// Package membership is who is here, on two timescales, decided against one
// clock — the local one.
//
// A service that places data by computation (pkg/v2/placement) needs a member
// list, and the member list is the ONLY input to the answer. So the shape of
// this package decides two unrelated things at once, and conflating them is
// what v1 paid for:
//
//	LIVE      may I ack a write to this peer RIGHT NOW.  1s beat, 5s expiry.
//	STABLE    should this peer OWN this data.            60s of continuous
//	                                                     presence or absence.
//
// One timescale cannot answer both. A peer that restarts and is back in four
// seconds has never stopped being a placement target, and moving its share of
// a fleet's bytes twice — out and back — is the most expensive thing a storage
// service can do to itself. v1's hoarder had one timescale and paid for it in
// placement thrash. Here a node that flaps for 30 seconds moves zero bytes,
// and that is a test in this package rather than a hope.
//
// # The clock is local and it is the only one
//
// Observe takes no timestamp. A Beat carries WHO and an incarnation and one
// declaration, and nothing that says WHEN — so a peer cannot advance its own
// expiry by lying about the time, and a fleet cannot be dragged around by one
// node with a broken clock. Liveness is now() minus the local instant this
// process received the beat. TestABeatCarriesNoClockOfItsOwn is the census
// that keeps it that way.
//
// This is a different question from the HLC in a storage version record, which
// must be comparable ACROSS nodes and therefore cannot be local. Liveness never
// crosses a node boundary: each node decides for itself who it may ack to, and
// two nodes legitimately disagreeing under an asymmetric partition is
// information rather than a bug — v1 found that liveness rides multi-hop
// gossip while a durability barrier needs a direct dial, and the two disagree
// exactly when it matters (services/hoarder/kvdb.go:292-307).
//
// # What is not decided here
//
// HOW BEATS ARRIVE. This package has no pubsub, no host, no RPC and no
// goroutine. It is a state machine over Observe, Evict and a clock. That is
// what makes "a node that flaps for 30s moves zero bytes" a fast unit test
// instead of a network fixture, and it is what lets the cluster service, when
// it exists, become the thing that calls Observe without any consumer of this
// package changing.
//
// WHETHER A MEMBER IS TRUSTED. A beat is a claim. The only claim a member gets
// to make about itself here is Accepts, and Accepts can only ever remove it
// from a placement set (pkg/v2/placement). There is no capacity, no load and no
// weight, so there is nothing to lie usefully about.
package membership

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/v2/placement"
)

// ErrStaleIncarnation is returned by Observe for a beat from an incarnation
// older than the one already recorded for that peer: a replay, or a message
// that overtook a restart. It is not a failure of anything — the caller drops
// the message — but it is returned rather than swallowed so a caller that is
// seeing a storm of them can say so.
var ErrStaleIncarnation = errors.New("membership: beat from a stale incarnation")

// Beat is one heartbeat, as received.
//
// THERE IS NO TIMESTAMP AND NO CAPACITY, and both absences are load-bearing —
// see the package doc. Adding either fails TestABeatCarriesNoClockOfItsOwn.
type Beat struct {
	// Peer is the sender's identity.
	Peer string

	// Incarnation distinguishes one run of a peer from the next. A peer picks
	// it at boot and never lowers it, so a beat carrying a lower incarnation
	// than the one on record is stale and is refused. It is NOT a clock: it is
	// never compared across peers and never converted to a time.
	Incarnation uint64

	// Accepts is false when the sender is declining new placement — full,
	// degraded, or shutting down. It rides the heartbeat because a decline
	// must reach every node that might place data on this one, and the
	// heartbeat is already that fan-out.
	Accepts bool
}

// Member is one peer's current state.
type Member struct {
	Peer        string
	Incarnation uint64
	Accepts     bool

	// Live is true when a beat arrived within the liveness window.
	Live bool
	// Stable is true when this peer has been continuously live for at least
	// the stabilize window, and has not since been continuously absent for
	// that long. It is the placement timescale.
	Stable bool
}

// Options are the two timescales and the clock. Zero means "the default",
// because a caller that has an opinion about only one of them should not have
// to restate the others.
type Options struct {
	// Liveness is how long after its last beat a peer is still an
	// acknowledgement target. Default 5s, against a 1s beat: four missed
	// beats.
	Liveness time.Duration

	// Stabilize is how long a peer must be continuously present before it
	// enters the placement set, and continuously absent before it leaves.
	// Default 60s.
	Stabilize time.Duration

	// Forget is how long an absent peer is remembered before it is dropped
	// entirely. It must exceed Stabilize, or a peer would be forgotten while
	// it was still in the placement set and would silently re-enter as a
	// newcomer. Default 10x Stabilize.
	Forget time.Duration

	// Now is the local clock. Tests inject one; production leaves it nil and
	// gets time.Now. It is never a peer's clock — see the package doc.
	Now func() time.Time
}

// Set is a membership view. It is safe for concurrent use.
type Set struct {
	opts Options

	mu    sync.Mutex
	peers map[string]*state
}

type state struct {
	incarnation uint64
	accepts     bool

	// lastBeat is the LOCAL instant the most recent beat arrived.
	lastBeat time.Time
	// liveSince is the local instant this peer most recently became live
	// after not being live. Zero while it is not live.
	liveSince time.Time
	// absentSince is the local instant this peer stopped being live. Zero
	// while it is live.
	absentSince time.Time
	// stable is the placement-timescale answer, carried rather than derived,
	// because it is hysteretic: it depends on how long the peer has been in
	// its current presence state, not merely on whether it is live now.
	stable bool
}

// New builds a membership view.
func New(opts Options) (*Set, error) {
	if opts.Liveness <= 0 {
		opts.Liveness = 5 * time.Second
	}
	if opts.Stabilize <= 0 {
		opts.Stabilize = 60 * time.Second
	}
	if opts.Forget <= 0 {
		opts.Forget = 10 * opts.Stabilize
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Stabilize < opts.Liveness {
		return nil, fmt.Errorf("membership: stabilize (%s) is shorter than liveness (%s): placement would move faster than acknowledgement", opts.Stabilize, opts.Liveness)
	}
	if opts.Forget <= opts.Stabilize {
		return nil, fmt.Errorf("membership: forget (%s) does not exceed stabilize (%s): a peer would be forgotten while it was still placed", opts.Forget, opts.Stabilize)
	}
	return &Set{opts: opts, peers: map[string]*state{}}, nil
}

// Observe records a heartbeat. It is the only way a peer enters the view.
func (s *Set) Observe(b Beat) error {
	if b.Peer == "" {
		return errors.New("membership: a beat with no sender")
	}

	now := s.opts.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.advance(now)

	st, known := s.peers[b.Peer]
	if !known {
		s.peers[b.Peer] = &state{
			incarnation: b.Incarnation,
			accepts:     b.Accepts,
			lastBeat:    now,
			liveSince:   now,
		}
		return nil
	}
	if b.Incarnation < st.incarnation {
		return fmt.Errorf("%w: %s at %d, on record at %d", ErrStaleIncarnation, b.Peer, b.Incarnation, st.incarnation)
	}

	// A HIGHER INCARNATION IS A RESTART, NOT A NEW PEER. It refreshes
	// liveness and it does not reset stability: the peer id is unchanged, so
	// HRW gives it exactly the shards it had, and re-homing them out and back
	// because a process restarted is the thrash this package exists to avoid.
	// What it does do is make the restart visible — Member.Incarnation moves —
	// so a caller that must refill a peer whose disk is gone can see it.
	st.incarnation = b.Incarnation
	st.accepts = b.Accepts
	if !live(st, now, s.opts.Liveness) {
		st.liveSince = now
		st.absentSince = time.Time{}
	}
	st.lastBeat = now
	return nil
}

// Evict drops a peer immediately, on both timescales. It is for what the
// transport knows and a missed beat does not say yet — a clean shutdown
// message, a closed connection. It is a LOCAL decision: nothing about eviction
// propagates, and another node that has not evicted the peer is not wrong.
func (s *Set) Evict(peer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.peers, peer)
}

// Live is who may be an acknowledgement target right now, ordered by peer id.
func (s *Set) Live() []Member {
	return s.snapshot(func(m Member) bool { return m.Live })
}

// Stable is who owns data, ordered by peer id. It is the input to placement,
// and it is deliberately NOT the same list as Live.
func (s *Set) Stable() []Member {
	return s.snapshot(func(m Member) bool { return m.Stable })
}

// All is every peer still remembered, live or not, ordered by peer id.
func (s *Set) All() []Member {
	return s.snapshot(func(Member) bool { return true })
}

// Get returns one peer's state, and whether it is remembered at all.
func (s *Set) Get(peer string) (Member, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	s.advance(now)
	st, ok := s.peers[peer]
	if !ok {
		return Member{}, false
	}
	return member(peer, st, now, s.opts.Liveness), true
}

// LivePlacement and StablePlacement hand a member list to pkg/v2/placement in
// the shape that package takes. They exist so no caller writes the Accepts
// mapping slightly differently — a caller that dropped it would be scoring a
// declining node into a placement set, which is the one direction the decline
// mechanism must never fail in.
func (s *Set) LivePlacement() []placement.Member   { return forPlacement(s.Live()) }
func (s *Set) StablePlacement() []placement.Member { return forPlacement(s.Stable()) }

func forPlacement(ms []Member) []placement.Member {
	out := make([]placement.Member, 0, len(ms))
	for _, m := range ms {
		out = append(out, placement.Member{ID: m.Peer, Accepts: m.Accepts})
	}
	return out
}

func (s *Set) snapshot(keep func(Member) bool) []Member {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	s.advance(now)

	out := make([]Member, 0, len(s.peers))
	for peer, st := range s.peers {
		m := member(peer, st, now, s.opts.Liveness)
		if keep(m) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Peer < out[j].Peer })
	return out
}

// advance runs the presence state machine up to now. It is called from every
// door, which is why there is no goroutine and no Tick: the two predicates it
// computes are monotone in now, so evaluating them lazily at read time gives
// the same answer as evaluating them continuously.
//
// s.mu must be held.
func (s *Set) advance(now time.Time) {
	for peer, st := range s.peers {
		isLive := live(st, now, s.opts.Liveness)

		switch {
		case isLive:
			// Presence continues. liveSince was set when it began.
			if now.Sub(st.liveSince) >= s.opts.Stabilize {
				st.stable = true
			}
		default:
			// Absence began the instant the liveness window closed, which is
			// a fixed offset from the last beat rather than "when somebody
			// happened to look".
			if st.absentSince.IsZero() {
				st.absentSince = st.lastBeat.Add(s.opts.Liveness)
				st.liveSince = time.Time{}
			}
			if now.Sub(st.absentSince) >= s.opts.Stabilize {
				st.stable = false
			}
			if now.Sub(st.absentSince) >= s.opts.Forget {
				delete(s.peers, peer)
			}
		}
	}
}

func live(st *state, now time.Time, window time.Duration) bool {
	return now.Sub(st.lastBeat) < window
}

func member(peer string, st *state, now time.Time, window time.Duration) Member {
	return Member{
		Peer:        peer,
		Incarnation: st.incarnation,
		Accepts:     st.accepts,
		Live:        live(st, now, window),
		Stable:      st.stable,
	}
}
