package kvdb

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"time"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"google.golang.org/protobuf/proto"
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

// dropClock is the time this replica forgets by. While rejoining it stands at
// the fleet time heard before the gap: a replica out of contact forgets
// nothing, so what it vouches for is judged by what it knew when it left. The
// caller holds store.mu.
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

// dropClockNow is dropClock for a caller not holding store.mu.
func (store *Datastore) dropClockNow() uint64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.dropClock()
}

// knownSince is the fleet time last heard, or the clock for a replica that
// never heard a peer, whose state holds nothing of anyone's that could be
// stale. The caller holds store.mu.
func (store *Datastore) knownSince() uint64 {
	if store.since == 0 {
		return store.clock()
	}
	return store.since
}

// hearFleet takes a peer's word for the fleet's time, no further ahead of the
// clock than the skew. The first time, everything held was written before
// hearing anyone and nothing logged it: once an element has expired by a
// margin the fleet may have removed it and forgotten the removal, so such a
// replica rejoins from its oldest element's time instead. One whose elements
// are all younger takes the time and logs them as its own operations.
func (store *Datastore) hearFleet(ctx context.Context, stamp uint64) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	stamp = min(stamp, store.clock()+store.times.skew)
	if store.rejoining || stamp <= store.since {
		return nil
	}
	t := newTxn(store.store)
	rejoin := false
	if store.since == 0 {
		held, err := store.heldElements(ctx)
		if err != nil {
			return err
		}
		oldest, expires := none, none
		for _, el := range held {
			oldest, expires = min(oldest, el.at), min(expires, el.expires())
		}
		switch {
		case oldest != none && store.expired(expires, stamp):
			stamp, rejoin = max(oldest, 1), true
		case oldest != none:
			if err := store.logHeld(t, held); err != nil {
				return err
			}
		}
	}
	t.put(store.sinceKey(), binary.AppendUvarint(nil, stamp))
	if err := t.commit(ctx); err != nil {
		return err
	}
	store.since, store.sinceUp = stamp, store.times.up()
	if rejoin {
		return store.startRejoin(ctx, stamp)
	}
	return nil
}

// The horizon is taken no further ahead of the clock than the skew. The caller
// holds store.mu.
func (store *Datastore) hearHorizon(ctx context.Context, h uint64) error {
	h = min(h, store.clock()+store.times.skew)
	if h <= store.horizon {
		return nil
	}
	if err := store.store.Put(ctx, store.horizonKey(), binary.AppendUvarint(nil, h)); err != nil {
		return err
	}
	store.horizon = h
	return nil
}

// The caller holds store.mu.
func (store *Datastore) heldElements(ctx context.Context) ([]element, error) {
	var held []element
	err := scanStore(ctx, store.store, store.set.elemsPrefix(), func(k string, v []byte) error {
		el, err := store.set.parseElem(k, v)
		if err != nil {
			return err
		}
		el.value = bytes.Clone(el.value)
		held = append(held, el)
		return nil
	})
	return held, err
}

// One record per dot, as logOwn would have written. The caller holds store.mu.
func (store *Datastore) logHeld(t *txn, held []element) error {
	recs := map[dot]*pb.Delta{}
	for _, el := range held {
		rec := recs[el.dot]
		if rec == nil {
			rec = &pb.Delta{At: el.at, Ttl: el.ttl, Priority: el.priority}
			recs[el.dot] = rec
		}
		rec.Elements = append(rec.Elements, &pb.Element{Key: bytesOf(el.key), Value: el.value})
	}
	for d, rec := range recs {
		b, err := proto.Marshal(rec)
		if err != nil {
			return err
		}
		t.put(store.ownKey(d), b)
		store.lows.own = min(store.lows.own, rec.GetAt()+rec.GetTtl())
	}
	return nil
}

// fleetNow is the later of the drop clock and the fleet time heard: what a
// judge compares expiries against. The caller holds store.mu.
func (store *Datastore) fleetNow() uint64 {
	return max(store.dropClock(), store.since)
}

func (store *Datastore) expired(expires uint64, now uint64) bool {
	return expires+store.times.margin <= now
}

var errAhead = errors.New("stamped ahead of this replica's clock")

func (store *Datastore) ahead(at uint64) bool {
	return at > store.clock()+store.times.skew
}

// carriesAhead reports whether a snapshot holds anything stamped ahead, written
// by a clock far ahead and carried once it was put right. It waits as the
// writing block would have, so a wrong clock's stamps never reach a correct
// state; the trees' summaries give each tree's latest stamp.
func (store *Datastore) carriesAhead(ctx context.Context, root *pb.Delta, dags ipld.DAGService) (bool, error) {
	stc, err := cid.Cast(root.GetState())
	if err != nil {
		return false, err
	}
	sr, err := loadStateRoot(ctx, dags, stc)
	if err != nil {
		return false, err
	}
	for _, c := range []cid.Cid{sr.elems, sr.pending, sr.ctx} {
		_, hi, any, err := treeStamps(ctx, store.tree(dags, c))
		if err != nil {
			return false, err
		}
		if any && store.ahead(hi) {
			return true, nil
		}
	}
	return false, nil
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
	for _, k := range store.erased {
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

// forget drops what has expired: incarnations no longer needed, dots a rebase
// erased, and pending removals no longer needed. A removal is needed as long
// as its target, whose delta has expired too once it does and is never merged;
// kept for a dot that never comes, it would be kept forever. It reports
// whether anything went.
func (m *merging) forget(ctx context.Context, now uint64) (bool, error) {
	dropped := false
	for r, k := range m.cc.inc {
		if m.store.expired(k.expires, now) {
			delete(m.cc.inc, r)
			m.horizon = max(m.horizon, k.changed)
			m.store.forgotten[r] = &forgottenEntry{k: k, at: m.store.times.up()}
			dropped = true
		}
	}
	for r, k := range m.store.erased {
		if m.store.expired(k.expires, now) && !slices.Contains(m.unerased, r) {
			m.t.del(m.store.erasedKey(r))
			m.unerased = append(m.unerased, r)
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

// expireLocked asks for a snapshot that no longer carries what it forgot. The
// caller holds store.mu.
func (store *Datastore) expireLocked(ctx context.Context) (bool, error) {
	m := store.beginMerge()
	defer m.t.endReads()
	dropped, err := m.forget(ctx, store.dropClock())
	if err != nil || !dropped {
		return false, err
	}
	if err := m.commit(ctx); err != nil {
		return false, err
	}
	store.compactDue = true
	return true, nil
}

// purgeAncient takes every local block no context covers whose expiry has
// passed, for dropTaken: no replica in contact needs it and no link to it is
// followed. Heads stay until a block links them. The caller holds store.mu.
func (store *Datastore) purgeAncient(ctx context.Context) ([]cid.Cid, error) {
	now := store.dropClock()
	// A block expires a margin past its whole ttl, after its half-life.
	if store.lows.block > now {
		return nil, nil
	}
	return store.take(ctx, func(b block) bool {
		return !store.cc.covers(b.dot) && store.expired(b.at+b.ttl, now)
	})
}

// gone: its dot is not covered and its expiry has passed, so every holder has
// purged it. The caller holds store.mu or reads the context without it.
func (store *Datastore) gone(l link, now uint64) bool {
	return !store.covered(l.dot) && store.expired(l.at+l.ttl, now)
}

// refreshDue reports whether a held block is past half its retention, to be
// folded into a new snapshot before any replica stops following links to it,
// or a head snapshot is stale (see staleHead). The caller holds store.mu.
func (store *Datastore) refreshDue(ctx context.Context) (bool, error) {
	if stale, err := store.staleHead(ctx); err != nil || stale {
		return stale, err
	}
	now := store.clock()
	if store.lows.block > now {
		return false, nil
	}
	due := false
	low := none
	prefix := store.blocksPrefix()
	err := scanStore(ctx, store.store, prefix, func(k string, v []byte) error {
		if store.isTaken(prefix, k) {
			return nil
		}
		b, err := decodeBlock(v)
		if err != nil {
			return err
		}
		low = min(low, b.at+b.ttl/2)
		if b.at+b.ttl/2 <= now {
			due = true
		}
		return nil
	})
	if err == nil && !due {
		store.lows.block = low
	}
	return due, err
}

// staleHead reports whether a head is a snapshot that this replica, by what
// it knows now, would refuse as stale were it another's, and one it wrote
// now would not be. A replica that knows no more than this one refuses that
// head too, and while it is a head here nothing else carries what it holds:
// a snapshot another replica wrote before it last heard the fleet, or one
// this replica wrote while it rejoined. The caller holds store.mu.
func (store *Datastore) staleHead(ctx context.Context) (bool, error) {
	if store.rejoining || store.since == 0 {
		return false, nil
	}
	hs, _ := store.heads.List()
	for _, h := range hs {
		v, err := store.store.Get(ctx, store.blockKey(h.Cid))
		if errors.Is(err, ds.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		b, err := decodeBlock(v)
		if err != nil {
			return false, err
		}
		if b.kind != kindSnapshot {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, store.opts.DAGSyncerTimeout)
		ng := &crdtNodeGetter{NodeGetter: store.dagService}
		_, root, err := ng.GetDelta(cctx, h.Cid)
		var since uint64
		if err == nil {
			since, err = store.snapshotSince(cctx, root, store.dagService)
		}
		cancel()
		if err != nil {
			return false, err
		}
		stale, err := store.stale(ctx, since)
		if err != nil {
			return false, err
		}
		if stale {
			behind, err := store.stale(ctx, store.since)
			return !behind, err
		}
	}
	return false, nil
}

// snapshotSince is a snapshot's author's fleet time. One written before its
// author heard any fleet holds only the author's writes and is as recent as
// the oldest of them, which the elements tree's summary gives.
func (store *Datastore) snapshotSince(ctx context.Context, root *pb.Delta, dags ipld.DAGService) (uint64, error) {
	since := root.GetSince()
	if since != 0 {
		return since, nil
	}
	since = root.GetAt()
	stc, err := cid.Cast(root.GetState())
	if err != nil {
		return 0, err
	}
	sr, err := loadStateRoot(ctx, dags, stc)
	if err != nil {
		return 0, err
	}
	lo, _, any, err := treeStamps(ctx, store.tree(dags, sr.elems))
	if err != nil {
		return 0, err
	}
	if any {
		since = min(since, lo)
	}
	return since, nil
}

// maintain forgets what expired, drops state fetches and blocks nobody needs,
// and compacts when a held block is half way to its expiry or something the
// last snapshot carries was forgotten.
func (store *Datastore) maintain(ctx context.Context) error {
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return ErrClosed
	}
	dropped, err := store.expireLocked(ctx)
	var ancient []cid.Cid
	if err == nil {
		ancient, err = store.purgeAncient(ctx)
	}
	if err == nil {
		err = store.settleOwn(ctx)
	}

	due := dropped || store.compactDue
	store.compactDue = false
	if err == nil && !due {
		due, err = store.refreshDue(ctx)
	}
	dropClock := store.dropClock()
	store.mu.Unlock()
	store.sweepFetches(ctx, dropClock)
	if _, derr := store.dropTaken(ctx, ancient); err == nil {
		err = derr
	}
	if _, rerr := store.reclaim(ctx); err == nil {
		err = rerr
	}
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
