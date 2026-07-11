package hoarder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"

	"github.com/fxamacker/cbor/v2"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/taubyte/tau/clients/p2p/seer/usage"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
)

// heartbeat is what a hoarder broadcasts on MembersTopic every
// HeartbeatInterval. Incarnation distinguishes a restarted node from its ghost;
// Zone/Capacity are self-reported placement signals.
type heartbeat struct {
	PeerID      string
	Incarnation int64
	Seq         uint64
	Zone        string
	Capacity    uint64
}

// member is the local view of another hoarder: its last heartbeat plus when we
// last heard it (local clock — never trust a peer's wall clock for liveness).
type member struct {
	hb       heartbeat
	lastSeen time.Time
}

// startMembership brings up the heartbeat/liveness controller: stamp a fresh
// incarnation, subscribe to the members topic, announce ourselves on a ticker,
// and expire silent peers. Any change to the active set fires onMembershipChange
// (→ reconcile). The incarnation stamp and the two loops are one-time start work
// and live only here — a mid-life subscription drop re-subscribes (see
// subscribeMembership) without restarting them, so hbSeq keeps its single-writer
// invariant and peers never see a spurious new incarnation.
func (srv *Service) startMembership(ctx context.Context) error {
	srv.incarnation = time.Now().UnixNano()

	if err := srv.subscribeMembership(ctx); err != nil {
		return err
	}

	srv.loopsWG.Add(2)
	go func() { defer srv.loopsWG.Done(); srv.heartbeatLoop(ctx) }()
	go func() { defer srv.loopsWG.Done(); srv.livenessLoop(ctx) }()
	return nil
}

// subscribeMembership opens (or, from its own error callback, re-opens) the
// members-topic subscription. It carries none of startMembership's one-time
// work: the error path re-subscribes only, so a gossipsub-internal reader
// failure can't spawn a second heartbeat/liveness loop or reset our incarnation.
func (srv *Service) subscribeMembership(ctx context.Context) error {
	return srv.node.PubSubSubscribe(
		hoarderSpecs.MembersTopic,
		func(msg *pubsub.Message) {
			hb := new(heartbeat)
			if cbor.Unmarshal(msg.Data, hb) != nil || hb.PeerID == "" {
				return
			}
			srv.observeHeartbeat(hb)
		},
		func(err error) {
			if ctx.Err() == nil {
				logger.Error("members subscription ended with:", err.Error())
				srv.resubscribe(ctx, "members", srv.subscribeMembership)
			}
		},
	)
}

// resubscribe re-establishes a pubsub subscription after its reader goroutine
// exited on a mid-life error, with a few bounded attempts to ride out a
// transient gossipsub hiccup. Each successful subscription spawns a fresh
// reader, so this is self-healing over the node's life without ever recursing
// unbounded on the stack. If every attempt fails the subsystem loses its event
// path and only the reconcile backstop keeps placement moving — a degraded state
// we log loudly rather than leave a dead subsystem silent.
func (srv *Service) resubscribe(ctx context.Context, topic string, subscribe func(context.Context) error) {
	// A short backoff between attempts; the retry only fires on the rare mid-life
	// drop, so it needn't be a shared tunable.
	const (
		attempts = 3
		backoff  = 200 * time.Millisecond
	)
	for i := 1; i <= attempts; i++ {
		if ctx.Err() != nil {
			return
		}
		err := subscribe(ctx)
		if err == nil {
			return
		}
		logger.Errorf("resubscribing to %s topic failed (attempt %d/%d) with: %s", topic, i, attempts, err.Error())
		if i < attempts {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
		}
	}
	logger.Errorf("gave up resubscribing to %s topic after %d attempts; the reconcile backstop remains the safety net", topic, attempts)
}

func (srv *Service) observeHeartbeat(hb *heartbeat) {
	self := srv.node.ID().String()
	if hb.PeerID == self {
		return
	}
	srv.membersLock.Lock()
	existing, known := srv.members[hb.PeerID]
	// Ignore an older incarnation's straggling beat.
	if known && hb.Incarnation < existing.hb.Incarnation {
		srv.membersLock.Unlock()
		return
	}
	srv.members[hb.PeerID] = &member{hb: *hb, lastSeen: time.Now()}
	srv.membersLock.Unlock()

	if !known {
		// A new hoarder appeared — its arrival can change who owns what.
		srv.onMembershipChange()
	}
}

func (srv *Service) heartbeatLoop(ctx context.Context) {
	beat := func() {
		srv.hbSeq++
		var capacity uint64
		if u, err := usage.GetUsage(); err == nil {
			capacity = u.Disk.Available
		}
		b, err := cbor.Marshal(&heartbeat{
			PeerID:      srv.node.ID().String(),
			Incarnation: srv.incarnation,
			Seq:         srv.hbSeq,
			Zone:        srv.zone,
			Capacity:    capacity,
		})
		if err != nil {
			return
		}
		if err := srv.node.PubSubPublish(ctx, hoarderSpecs.MembersTopic, b); err != nil && ctx.Err() == nil {
			logger.Error("heartbeat publish failed with:", err.Error())
		}
	}
	beat() // announce immediately so a fresh node is discoverable fast
	ticker := time.NewTicker(hoarderSpecs.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			beat()
		}
	}
}

// livenessLoop drops members we haven't heard from within LivenessTimeout (a
// few missed beats). A false positive — a live node whose beats were briefly
// lost — self-heals: its next heartbeat re-adds it and reconcile re-places it.
func (srv *Service) livenessLoop(ctx context.Context) {
	ticker := time.NewTicker(hoarderSpecs.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			srv.expireDead()
		}
	}
}

func (srv *Service) expireDead() {
	now := time.Now()
	srv.membersLock.Lock()
	changed := false
	for id, m := range srv.members {
		if now.Sub(m.lastSeen) > hoarderSpecs.LivenessTimeout {
			delete(srv.members, id)
			changed = true
		}
	}
	srv.membersLock.Unlock()
	if changed {
		srv.onMembershipChange()
	}
}

// activeMembers returns the sorted live hoarder set (self always included). This
// is the placement membership — HRW is computed over exactly this set.
func (srv *Service) activeMembers() []string {
	self := srv.node.ID().String()
	now := time.Now()
	srv.membersLock.RLock()
	out := make([]string, 0, len(srv.members)+1)
	out = append(out, self)
	for id, m := range srv.members {
		if now.Sub(m.lastSeen) <= hoarderSpecs.LivenessTimeout {
			out = append(out, id)
		}
	}
	srv.membersLock.RUnlock()
	sort.Strings(out)
	return out
}

// membershipEpoch is a short digest of the active set — stamped into decisions so
// stale-epoch actions can be recognised across a membership change.
func (srv *Service) membershipEpoch() string {
	h := sha256.Sum256([]byte(joinSorted(srv.activeMembers())))
	return hex.EncodeToString(h[:8])
}

func joinSorted(ss []string) string {
	out := ""
	for _, s := range ss {
		out += s + "\x00"
	}
	return out
}
