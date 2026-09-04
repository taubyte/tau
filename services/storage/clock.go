package storage

import (
	"fmt"
	"sync"
	"time"
)

// clock.go is the stamp a version row is keyed by, and the two refusals that
// keep a broken clock from becoming a data-loss event.
//
// A HYBRID LOGICAL CLOCK: 48 bits of physical milliseconds, then 16 bits of a
// logical counter, packed into the uint64 the worker ABI already expects
// (put -> u64, open(name, version), versions(name) -> list<u64>).
//
// WHAT IT BUYS. Causality without a leader: a writer that has SEEN an earlier
// version stamps strictly greater than it, because Observe pulls the local
// clock past every stamp it merges. Two writes with no causal path between
// them are ordered arbitrarily — but IDENTICALLY on every replica, because the
// order is byte order over the key and the key carries the stamp. That is the
// fourth guarantee in the brief's §1, and it is what makes "no write silently
// discarded" safe: two surviving writes need a winner every reader agrees on.
//
// WHAT IT IS NOT. It is an ADDRESS, not an identity. The identity of a version
// is its key — stamp AND writer. Two nodes writing in the same millisecond with
// the same logical counter mint the same u64 and still produce two distinct
// rows, resolved deterministically; what becomes ambiguous is only
// open(name, u64), and the fix for that class is an opaque string version id in
// the ABI rather than anything this file can do. Recorded in doc.go as a
// vocabulary finding, not papered over here.
//
// ── THE TWO REFUSALS ─────────────────────────────────────────────────────
//
// An HLC's known failure is one badly-skewed node dragging every clock in the
// fleet forward, permanently, because every other node advances past the stamp
// it saw. A design that omits this is incomplete, so:
//
//  1. A REMOTE STAMP FURTHER AHEAD THAN maxSkew DOES NOT MOVE THIS CLOCK.
//     Observe returns ErrRemoteClockSkew and advances nothing. The record is
//     still merged by whoever called — it must be, it is a CRDT and refusing it
//     would be discarding a write — so the skewed node's rows are all still
//     there and still resolve; what does not happen is the rest of the fleet
//     inheriting its clock.
//
//  2. A LOCAL CLOCK THAT HAS GONE BACKWARDS PAST maxSkew REFUSES TO MINT.
//     Now returns ErrLocalClockSkew. Because rule 1 bounds every accepted stamp
//     to at most local-now plus maxSkew at the moment it was accepted, and
//     time only moves forward, the physical component of this clock can only
//     exceed now()+maxSkew if THIS node's wall clock jumped backwards. That is
//     the one clock fault a node can diagnose about itself with no peer's help,
//     and it is the one that matters: minting from a backwards clock produces
//     stamps below ones already handed out, so a later write loses to an
//     earlier one on every replica at once.
//
// Neither refusal is a lock, an election or a round trip. They are two
// comparisons against the local clock.
//
// AND NO DELETION DECISION READS THIS CLOCK. gc.go times its grace window from
// when a node first OBSERVED a tombstone, recorded locally and monotonically,
// never from the stamp's physical part — so there is no clock trust anywhere on
// the path that destroys data.

const (
	// stampLogicalBits is the width of the logical counter inside the packed
	// stamp.
	stampLogicalBits = 16
	// stampMaxLogical is the largest logical counter a millisecond can carry.
	stampMaxLogical = 1<<stampLogicalBits - 1
	// stampMaxPhysical is the largest packable millisecond: 48 bits, which
	// runs out in the year 10889.
	stampMaxPhysical = 1<<(64-stampLogicalBits) - 1

	// DefaultMaxSkew is how far ahead of local time a remote stamp may be and
	// still move this node's clock.
	DefaultMaxSkew = 5 * time.Minute
)

// PackStamp builds the wire/key form of a stamp.
func PackStamp(physicalMS uint64, logical uint16) (uint64, error) {
	if physicalMS > stampMaxPhysical {
		return 0, fmt.Errorf("%w: %d ms does not fit in %d bits", ErrStampRange, physicalMS, 64-stampLogicalBits)
	}
	return physicalMS<<stampLogicalBits | uint64(logical), nil
}

// UnpackStamp splits a stamp back into its two components. It is total: every
// uint64 is a well-formed stamp, which is why there is no error here and one
// in PackStamp.
func UnpackStamp(stamp uint64) (physicalMS uint64, logical uint16) {
	return stamp >> stampLogicalBits, uint16(stamp & stampMaxLogical)
}

// Clock is one node's hybrid logical clock. Safe for concurrent use: minting a
// stamp is the one thing every concurrent write on a coordinator does.
type Clock struct {
	now     func() time.Time
	maxSkew time.Duration

	mu       sync.Mutex
	physical uint64
	logical  uint16
}

// NewClock builds a clock. A nil now is time.Now; a non-positive maxSkew is
// DefaultMaxSkew — a caller with an opinion about one should not have to
// restate the other.
func NewClock(now func() time.Time, maxSkew time.Duration) *Clock {
	if now == nil {
		now = time.Now
	}
	if maxSkew <= 0 {
		maxSkew = DefaultMaxSkew
	}
	return &Clock{now: now, maxSkew: maxSkew}
}

// Now mints the next stamp: strictly greater than every stamp this clock has
// minted or merged.
func (c *Clock) Now() (uint64, error) {
	physicalNow, err := c.physicalNow()
	if err != nil {
		return 0, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.physical > physicalNow+c.skewMS() {
		return 0, fmt.Errorf("%w: clock is at %d ms, stamps reach %d ms", ErrLocalClockSkew, physicalNow, c.physical)
	}

	switch {
	case physicalNow > c.physical:
		c.physical, c.logical = physicalNow, 0
	case c.logical == stampMaxLogical:
		// Wrapping would mint a stamp BELOW one already returned to a caller,
		// which is a write that loses to its own predecessor on every replica.
		// Refusing is the only answer that does not lose data.
		return 0, fmt.Errorf("%w: %d stamps at %d ms", ErrLogicalOverflow, stampMaxLogical+1, c.physical)
	default:
		c.logical++
	}
	return PackStamp(c.physical, c.logical)
}

// Observe advances this clock past a stamp seen on a remote record, so a
// writer that has seen a version stamps strictly greater than it.
//
// It returns ErrRemoteClockSkew, and advances nothing, for a stamp whose
// physical part is further ahead of local time than the skew budget. THE
// CALLER STILL MERGES THE RECORD: refusing to merge would be discarding a
// write, which is the one thing this service does not do.
func (c *Clock) Observe(stamp uint64) error {
	physicalNow, err := c.physicalNow()
	if err != nil {
		return err
	}
	remotePhysical, remoteLogical := UnpackStamp(stamp)

	if remotePhysical > physicalNow+c.skewMS() {
		return fmt.Errorf("%w: stamp at %d ms, clock at %d ms, budget %s", ErrRemoteClockSkew, remotePhysical, physicalNow, c.maxSkew)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	switch {
	case remotePhysical > c.physical:
		c.physical, c.logical = remotePhysical, remoteLogical
	case remotePhysical == c.physical && remoteLogical > c.logical:
		c.logical = remoteLogical
	}
	return nil
}

// Peek reports the clock's current state without advancing it. It exists for
// tests and for an operator read; nothing on a write path calls it.
func (c *Clock) Peek() (physicalMS uint64, logical uint16) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.physical, c.logical
}

func (c *Clock) skewMS() uint64 { return uint64(c.maxSkew / time.Millisecond) }

func (c *Clock) physicalNow() (uint64, error) {
	ms := c.now().UnixMilli()
	if ms < 0 {
		return 0, fmt.Errorf("%w: local clock reads %d ms, before the epoch", ErrStampRange, ms)
	}
	if uint64(ms) > stampMaxPhysical {
		return 0, fmt.Errorf("%w: local clock reads %d ms", ErrStampRange, ms)
	}
	return uint64(ms), nil
}
