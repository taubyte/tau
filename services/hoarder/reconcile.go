package hoarder

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"github.com/fxamacker/cbor/v2"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
)

// reconcileMsg is a change notification: reconcile Hash now. Meta is embedded so
// a receiver can act before the CRDT registry has propagated the record (the
// fast path); it's nil for claim/delete notifications where the receiver reads
// current registry state.
type reconcileMsg struct {
	Hash string
	Meta *RegistryMeta
}

// startReconcile wires the placement control loop: create the trigger queue,
// subscribe to change notifications, and run the serial reconcile loop (one
// instance at a time — no auction storms). The queue and the loop are one-time
// start work and live only here — a mid-life subscription drop re-subscribes
// (see subscribeReconcile) without reallocating reconcileTrigger under the
// running loop or spawning a second loop, so the "serial reconcile" invariant
// holds.
func (srv *Service) startReconcile(ctx context.Context) error {
	// Make the queue before subscribing: the pubsub handler enqueues onto it.
	srv.reconcileTrigger = make(chan string, 1024)

	if err := srv.subscribeReconcile(ctx); err != nil {
		return err
	}

	srv.loopsWG.Add(1)
	go func() { defer srv.loopsWG.Done(); srv.reconcileLoop(ctx) }()
	return nil
}

// subscribeReconcile opens (or, from its own error callback, re-opens) the
// reconcile-topic subscription. It carries none of startReconcile's one-time
// work: the error path re-subscribes only, so a gossipsub-internal reader
// failure can't reallocate reconcileTrigger under the running loop or spawn a
// second reconcile loop.
func (srv *Service) subscribeReconcile(ctx context.Context) error {
	return srv.node.PubSubSubscribe(
		hoarderSpecs.ReconcileTopic,
		func(msg *pubsub.Message) {
			m := new(reconcileMsg)
			if cbor.Unmarshal(msg.Data, m) != nil || m.Hash == "" {
				return
			}
			// Seed the record locally so reconcile can act without waiting on CRDT
			// propagation (idempotent, write-on-change).
			if m.Meta != nil {
				_ = srv.putMeta(ctx, m.Hash, m.Meta)
			}
			srv.enqueueReconcile(m.Hash)
		},
		func(err error) {
			if ctx.Err() == nil {
				logger.Error("reconcile subscription ended with:", err.Error())
				srv.resubscribe(ctx, "reconcile", srv.subscribeReconcile)
			}
		},
	)
}

func (srv *Service) enqueueReconcile(hash string) {
	select {
	case srv.reconcileTrigger <- hash:
	default: // full queue: the backstop will catch it
	}
}

// onMembershipChange is the membership controller's callback: a fleet change can
// move ownership of every resource, so re-reconcile all.
func (srv *Service) onMembershipChange() {
	srv.enqueueReconcile("")
}

// publishReconcile notifies the fleet that a resource changed. Best-effort: a
// dropped notification is covered by the backstop.
func (srv *Service) publishReconcile(ctx context.Context, hash string, meta *RegistryMeta) {
	b, err := cbor.Marshal(&reconcileMsg{Hash: hash, Meta: meta})
	if err != nil {
		return
	}
	if err := srv.node.PubSubPublish(ctx, hoarderSpecs.ReconcileTopic, b); err != nil && ctx.Err() == nil {
		logger.Error("reconcile publish failed with:", err.Error())
	}
}

func (srv *Service) reconcileLoop(ctx context.Context) {
	backstop := time.NewTicker(hoarderSpecs.ReconcileBackstop)
	defer backstop.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case hash := <-srv.reconcileTrigger:
			if hash == "" {
				srv.reconcileAll(ctx)
			} else {
				srv.reconcileOne(ctx, hash)
			}
		case <-backstop.C:
			srv.reconcileAll(ctx)
		}
	}
}

// reconcileAll re-checks every resource the registry knows about (backstop and
// membership-change path). Small-to-medium fleets: a full scan is cheap and the
// registry is the source of truth.
func (srv *Service) reconcileAll(ctx context.Context) {
	hashes, err := srv.listMetaHashes(ctx)
	if err != nil {
		logger.Error("reconcileAll: listing meta failed with:", err.Error())
		return
	}
	// Also cover anything we still hold whose meta may have vanished.
	seen := make(map[string]bool, len(hashes))
	for _, h := range hashes {
		seen[h] = true
		srv.reconcileOne(ctx, h)
	}
	for _, h := range srv.claimedHashes() {
		if !seen[h] {
			srv.reconcileOne(ctx, h)
		}
	}

	srv.stashReconcile(ctx)
}

// stashReconcile re-fans-out any stashed CID this node holds that has fallen
// below the replica target — the self-heal for a stash whose initial fan-out
// happened before the fleet was fully known, or a claimant that later died.
func (srv *Service) stashReconcile(ctx context.Context) {
	mine, err := srv.stashClaimedByMe(ctx)
	if err != nil {
		return
	}
	target := targetReplicas(srv.fleetSize())
	for _, cid := range mine {
		claims, err := srv.listStashClaims(ctx, cid)
		if err != nil {
			continue
		}
		if len(srv.liveClaimants(claims)) >= target {
			continue
		}
		owner := ""
		if meta, err := srv.getStashMeta(ctx, cid); err == nil {
			owner = meta.OwnerHash
		}
		srv.fanoutStash(ctx, cid, target, owner)
	}
}

// reconcileOne is the placement decision for a single instance: compute the
// deterministic desired owner set (HRW over live membership) and converge this
// node's claim toward it. No auction — every node reaches the same desired set.
func (srv *Service) reconcileOne(ctx context.Context, hash string) {
	meta, err := srv.getMeta(ctx, hash)
	if err != nil {
		// No record. If we were holding it, the resource was deleted — drop.
		// Otherwise there's nothing to place (or the record hasn't arrived yet).
		if srv.isClaimed(hash) {
			srv.dropLocal(ctx, hash)
		}
		return
	}

	if srv.configDeleted(meta) {
		srv.deleteResource(ctx, hash)
		srv.publishReconcile(ctx, hash, nil)
		return
	}

	members := srv.activeMembers()
	target := targetReplicas(len(members))
	desired := placementDesired(hash, members, target)
	self := srv.node.ID().String()
	inDesired := contains(desired, self)
	claimed := srv.isClaimed(hash)

	switch {
	case inDesired && !claimed:
		// A small jitter so co-owners reacting to the same change don't herd the
		// registry with simultaneous writes.
		if j := hoarderSpecs.ReconcileJitter; j > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(rand.Int63n(int64(j) + 1))):
			}
		}
		if err := srv.claimAndLoad(ctx, hash, metaAuction(meta)); err != nil {
			logger.Errorf("reconcile: claiming %s failed with: %s", hash, err)
			return
		}
		srv.publishReconcile(ctx, hash, meta)

	case !inDesired && claimed:
		// Conservative release: step back only once enough OTHER live holders
		// exist, so we never dip below target during a handoff.
		claims, err := srv.listClaims(ctx, hash)
		if err != nil {
			return
		}
		others := 0
		for _, c := range srv.liveClaimants(claims) {
			if c != self {
				others++
			}
		}
		if others >= target {
			srv.releaseClaim(ctx, hash, self) //nolint:errcheck
			srv.unload(hash)
			srv.unmarkClaimed(hash)
			srv.publishReconcile(ctx, hash, nil)
		}
	}
}

// recoverClaims rebuilds in-memory load state on boot: any registry claim
// bearing this node's PID is re-claimed and loaded. Without this a restarted
// hoarder would forget what it holds until the next reconcile.
func (srv *Service) recoverClaims(ctx context.Context) {
	self := srv.node.ID().String()
	hashes, err := srv.listMetaHashes(ctx)
	if err != nil {
		logger.Errorf("startup recovery: listing meta failed with: %s", err)
		return
	}
	for _, hash := range hashes {
		claims, err := srv.listClaims(ctx, hash)
		if err != nil {
			continue
		}
		for _, c := range claims {
			if c != self {
				continue
			}
			srv.markClaimed(hash)
			if _, err := srv.load(hash); err != nil {
				logger.Errorf("startup recovery: loading %s failed with: %s", hash, err)
			}
			break
		}
	}
}

// deleteResource drops this node's claim, the placement record, and the loaded
// instance when the backing config no longer exists (delete/rename). Idempotent
// across claimants — each drops its own claim; meta delete is write-on-change.
func (srv *Service) deleteResource(ctx context.Context, hash string) {
	self := srv.node.ID().String()
	srv.releaseClaim(ctx, hash, self) //nolint:errcheck
	srv.deleteMeta(ctx, hash)         //nolint:errcheck
	srv.unload(hash)
	srv.unmarkClaimed(hash)
}

// dropLocal releases in-memory state without touching the registry (used when
// the registry entry is already gone).
func (srv *Service) dropLocal(ctx context.Context, hash string) {
	self := srv.node.ID().String()
	srv.releaseClaim(ctx, hash, self) //nolint:errcheck
	srv.unload(hash)
	srv.unmarkClaimed(hash)
}

// configDeleted reports whether the backing TNS config is DEFINITIVELY gone
// (deleted or renamed) — the only signal on which deleteResource may tear down
// placement fleet-wide. It fires solely on errNoConfigMatch, i.e. TNS answered
// and no config matched. Every other validateConfig error is deliberately NOT a
// deletion:
//   - a listing failure is a transient TNS outage; acting on a blip would drop
//     claims and unload the instance on every hoarder, and if membership shifts
//     during the window new holders open empty stores. The reconcile backstop
//     re-checks once TNS answers again.
//   - an unknown Kind is record corruption, not evidence the config was removed;
//     purging placement on a garbled record could lose access to intact data, so
//     we leave it alone (claim paths still fail safe on the same error).
//
// Global has no backing TNS config to lose, so it is never config-deleted.
func (srv *Service) configDeleted(meta *RegistryMeta) bool {
	if meta.Kind == hoarderIface.Global {
		return false
	}
	return errors.Is(srv.validateConfig(metaAuction(meta)), errNoConfigMatch)
}

// metaAuction reconstructs the auction carrier for a registry entry (identity +
// kind) for config validation / claim.
func metaAuction(meta *RegistryMeta) *hoarderIface.Auction {
	return &hoarderIface.Auction{
		MetaType: meta.Kind,
		Meta: hoarderIface.MetaData{
			ConfigId:      meta.ConfigId,
			ProjectId:     meta.ProjectId,
			ApplicationId: meta.ApplicationId,
			Match:         meta.Match,
			Branch:        meta.Branch,
		},
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// dedup returns ss with duplicates removed, preserving first-seen order.
func dedup(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
