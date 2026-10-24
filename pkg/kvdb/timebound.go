package kvdb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
)

// What a replica knows is forgotten by time, and nothing else.
//
// Every block carries when it was written (at) and the retention T its author
// wrote it under (ttl). What a block teaches, a removal above all, is needed
// until at + ttl: after that, every replica that could still show the removed
// element has been away long enough to be judged stale and rebased (see
// rejoin.go). A replica forgets it a margin later, by its drop clock: an
// incarnation the context knows (retire), a pending removal of a dot the
// context does not cover, a block nobody needs. The latest authored time of
// anything it forgot is its horizon, which every block it writes carries.
//
// Because the expiry of each thing is written in the data, a change of T
// changes only what is written afterwards, and replicas that read different
// T values for a while still agree on when each thing expires.
//
// The drop clock is the wall clock, held back to the fleet time this replica
// has heard plus the time it has been up since: a clock far ahead cannot make
// a replica forget early. A replica that has never heard a peer has no fleet
// to protect and uses its clock.

const (
	sinceKey   = "s" // under the context: the fleet time last heard
	horizonKey = "h" // under the context: the horizon
)

func (store *Datastore) sinceKey() ds.Key {
	return store.namespace.ChildString(contextNs).ChildString(sinceKey)
}

func (store *Datastore) horizonKey() ds.Key {
	return store.namespace.ChildString(contextNs).ChildString(horizonKey)
}

func readMillis(ctx context.Context, store ds.Datastore, k ds.Key) (uint64, error) {
	v, err := store.Get(ctx, k)
	switch {
	case errors.Is(err, ds.ErrNotFound):
		return 0, nil
	case err != nil:
		return 0, err
	}
	n, k2 := binary.Uvarint(v)
	if k2 <= 0 {
		return 0, fmt.Errorf("error decoding %s", k)
	}
	return n, nil
}

// lows: see Datastore.lows. none is "nothing there".
type lows struct{ pending, block, own uint64 }

const none = ^uint64(0)

func (l *lows) lower(o lows) {
	l.pending = min(l.pending, o.pending)
	l.block = min(l.block, o.block)
	l.own = min(l.own, o.own)
}

// times are the durations the rules read, in milliseconds.
type times struct {
	margin uint64 // m
	window uint64 // D + 2S: how much earlier than a replica's since what it missed may be stamped
	skew   uint64 // S
	up     func() time.Duration
}

func timesOf(opts *Options) times {
	return times{
		margin: durationMillis(opts.margin()),
		window: durationMillis(opts.DeliveryBound + 2*opts.ClockSkew),
		skew:   durationMillis(opts.ClockSkew),
		up:     opts.Uptime,
	}
}

// dropClock is the time this replica forgets by. The caller holds store.mu.
//
// While rejoining it stands still at the fleet time heard before the gap: a
// replica out of contact forgets nothing, so that what it vouches for, and
// what another rejoining replica rebuilt from it holds, is judged by what it
// knew when it left.
func (store *Datastore) dropClock() uint64 {
	now := store.clock()
	switch {
	case store.since == 0:
		return now
	case store.rejoining:
		return min(now, store.since)
	}
	return min(now, store.since+durationMillis(store.times.up()-store.sinceUp))
}

// dropClockNow is dropClock for a caller that does not hold store.mu: a
// walk deciding which links name blocks nobody holds any more.
func (store *Datastore) dropClockNow() uint64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.dropClock()
}

// knownSince is how recent what this replica holds is: the fleet time it last
// heard, or its clock if it has never heard a peer, since then it holds
// nothing of anyone else's that could have gone stale. The caller holds
// store.mu.
func (store *Datastore) knownSince() uint64 {
	if store.since == 0 {
		return store.clock()
	}
	return store.since
}

// hearFleet takes a peer's word for the fleet's time, no further ahead of
// this replica's clock than the skew.
func (store *Datastore) hearFleet(ctx context.Context, stamp uint64) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	stamp = min(stamp, store.clock()+store.times.skew)
	if store.rejoining || stamp <= store.since {
		return nil
	}
	if err := store.store.Put(ctx, store.sinceKey(), binary.AppendUvarint(nil, stamp)); err != nil {
		return err
	}
	store.since, store.sinceUp = stamp, store.times.up()
	return nil
}

// fleetNow is the latest of the drop clock and the fleet time heard: what a
// judge compares expiries against. The caller holds store.mu.
func (store *Datastore) fleetNow() uint64 {
	return max(store.dropClock(), store.since)
}

func (store *Datastore) expired(expires uint64, now uint64) bool {
	return expires+store.times.margin <= now
}

var errAhead = errors.New("stamped ahead of this replica's clock")

// ahead reports whether a block stamped at is further ahead of this
// replica's clock than the skew.
func (store *Datastore) ahead(at uint64) bool {
	return at > store.clock()+store.times.skew
}

// stale reports whether a state as recent as since may hold an element this
// replica could no longer refute: a removal written after since, less the
// window, that it has forgotten or is about to. The caller holds store.mu.
func (store *Datastore) stale(ctx context.Context, since uint64) (bool, error) {
	floor := uint64(0)
	if since > store.times.window {
		floor = since - store.times.window
	}
	if store.horizon != 0 && store.horizon >= floor {
		return true, nil
	}
	now := store.fleetNow()
	for _, k := range store.cc.inc {
		if k.changed >= floor && k.expires <= now {
			return true, nil
		}
	}
	if store.lows.pending > now {
		return false, nil
	}
	danger := false
	low := none
	err := scanStore(ctx, store.store, store.set.pendingPrefix(), func(k string, v []byte) error {
		r, err := store.set.parsePending(k, v)
		if err != nil {
			return err
		}
		low = min(low, r.expires())
		if r.at >= floor && r.expires() <= now {
			danger = true
		}
		return nil
	})
	if err == nil {
		store.lows.pending = low
	}
	return danger, err
}

// forget drops from a merge everything that has expired by now: the
// incarnations it no longer needs to know, and the pending removals no longer
// needed. A removal is needed as long as its target is, so once it expires its
// target's delta has expired too and is never merged (T8.12): a removal kept
// for a dot that never comes would be kept forever. It reports whether
// anything went.
func (m *merging) forget(ctx context.Context, now uint64) (bool, error) {
	dropped := false
	for r, k := range m.cc.inc {
		if m.store.expired(k.expires, now) {
			delete(m.cc.inc, r)
			m.horizon = max(m.horizon, k.changed)
			dropped = true
		}
	}
	if !m.store.expired(m.store.lows.pending, now) {
		return dropped, nil
	}
	pending, err := m.t.scan(ctx, m.store.set.pendingPrefix())
	if err != nil {
		return false, err
	}
	low := none
	for _, e := range pending {
		r, err := m.store.set.parsePending(e.key, e.value)
		if err != nil {
			return false, err
		}
		if m.store.expired(r.expires(), now) {
			m.t.del(ds.RawKey(e.key))
			m.horizon = max(m.horizon, r.at)
			dropped = true
		} else {
			low = min(low, r.expires())
		}
	}
	m.scanned.pending = low
	return dropped, nil
}

// expireLocked forgets what has expired. The caller holds store.mu.
func (store *Datastore) expireLocked(ctx context.Context) (bool, error) {
	m := store.beginMerge()
	dropped, err := m.forget(ctx, store.dropClock())
	if err != nil || !dropped {
		return false, err
	}
	return true, m.commit(ctx)
}

// purgeAncient drops every local block that no context covers any more and
// whose own expiry has passed: no replica in contact needs it, and a link to
// it is never followed. Heads stay until a block links them. The caller
// holds store.mu.
func (store *Datastore) purgeAncient(ctx context.Context) (int, error) {
	now := store.dropClock()
	// A block expires a margin past its whole ttl, after its half-life.
	if store.lows.block > now {
		return 0, nil
	}
	var drop []cid.Cid
	var keys []ds.Key
	prefix := store.blocksPrefix()
	err := scanStore(ctx, store.store, prefix, func(k string, v []byte) error {
		b, err := decodeBlock(v)
		if err != nil {
			return err
		}
		if store.cc.covers(b.dot) || !store.expired(b.at+b.ttl, now) {
			return nil
		}
		c, err := cidOfBlockKey(prefix, k)
		if err != nil {
			return err
		}
		if _, isHead := store.heads.Get(c); isHead {
			return nil
		}
		drop = append(drop, c)
		keys = append(keys, ds.RawKey(k))
		return nil
	})
	if err != nil || len(drop) == 0 {
		return 0, err
	}
	return len(drop), store.dropBlocks(ctx, drop, keys)
}

// gone reports whether a link names a block nobody needs any more: its dot is
// not covered here and its expiry has passed, so every holder has purged it.
// The caller holds store.mu or reads the context without it.
func (store *Datastore) gone(l link, now uint64) bool {
	return !store.covered(l.dot) && store.expired(l.at+l.ttl, now)
}

// refreshDue reports whether this replica holds a block, a snapshot root or a
// delta, past half of its retention: it is folded into a new snapshot before
// any replica stops following links to it. The caller holds store.mu.
func (store *Datastore) refreshDue(ctx context.Context) (bool, error) {
	now := store.clock()
	if store.lows.block > now {
		return false, nil
	}
	due := false
	low := none
	err := scanStore(ctx, store.store, store.blocksPrefix(), func(_ string, v []byte) error {
		b, err := decodeBlock(v)
		if err != nil {
			return err
		}
		low = min(low, b.at+b.ttl/2)
		if b.kind != kindChunk && b.at+b.ttl/2 <= now {
			due = true
		}
		return nil
	})
	if err == nil && !due {
		store.lows.block = low
	}
	return due, err
}

// maintain is one pass of what time does to a store: forget what expired,
// drop the blocks nobody needs, and compact when a held block is half way to
// its expiry or something the last snapshot carries has been forgotten.
func (store *Datastore) maintain(ctx context.Context) error {
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return ErrClosed
	}
	dropped, err := store.expireLocked(ctx)
	if err == nil {
		_, err = store.purgeAncient(ctx)
	}
	if err == nil {
		err = store.settleOwn(ctx)
	}

	due := dropped || store.compactDue
	store.compactDue = false
	if err == nil && !due {
		due, err = store.refreshDue(ctx)
	}
	store.mu.Unlock()
	if err != nil || !due {
		return err
	}
	_, err = store.compact(ctx, true)
	return err
}

func (store *Datastore) maintenance(ctx context.Context) {
	ticker := time.NewTicker(store.opts.RebroadcastInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := store.maintain(ctx); err != nil && !errors.Is(err, ErrClosed) && ctx.Err() == nil {
				store.logger.Warnf("maintenance: %s", err)
			}
		}
	}
}
