package storage

import (
	"fmt"
	"sync"
	"time"
)

// A hybrid logical clock: 48 bits of physical milliseconds and 16 bits of logical
// counter, packed into the uint64 the worker ABI expects. A writer that has seen a
// version stamps strictly greater than it (Observe); causally unrelated writes are
// ordered arbitrarily but identically everywhere, by key byte order. A stamp is an
// address, not an identity: the identity is stamp and writer.
//
// Two refusals keep a broken clock from losing data:
//
//  1. A remote stamp more than maxSkew ahead does not move this clock
//     (ErrRemoteClockSkew). The record is still merged; the fleet just does not
//     inherit the skewed clock.
//  2. A local clock gone backwards past maxSkew refuses to mint
//     (ErrLocalClockSkew). Since rule 1 bounds accepted stamps, the physical
//     component can only exceed now()+maxSkew if this node's wall clock jumped
//     back, and minting then would put later writes below earlier ones.
//
// No deletion decision reads this clock: gc.go times grace from local observation.

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

// Observe advances this clock past a remote stamp. A stamp too far ahead returns
// ErrRemoteClockSkew and advances nothing; the caller still merges the record.
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

// Floor raises this clock to physical milliseconds, whatever its local time says:
// the stamps this node minted before it last stopped. A wall clock further behind it
// than the skew budget then refuses to mint (ErrLocalClockSkew) rather than mint
// below them.
func (c *Clock) Floor(physicalMS uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if physicalMS > c.physical {
		c.physical, c.logical = physicalMS, 0
	}
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
