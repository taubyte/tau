package kvdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	cid "github.com/ipfs/go-cid"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// A stateFetch is the fetch of one state's closure, kept across attempts.
// An attempt is bounded by DAGSyncerTimeout, and a state larger than one
// attempt can fetch arrives over several: what an attempt fetched stays
// staged, and the next goes on from the blocks it was still waiting for.
// Nothing of it is visible until a merge acquires the closure.
//
// A fetch is dropped, and what it staged left to reclaim, once none of the
// snapshots it was begun for can be merged any more (covered or gone), once
// a newer snapshot of the same author has fetched its whole closure
// (superseded), or once it has fetched nothing for D: D bounds how long what
// a replica in contact holds takes to reach this one, so a fetch that has
// received nothing in D has no source in contact. A superseded fetch is kept
// until its successor's walk is done, and that walk reads what it fetched as
// held. A restart drops every fetch: their blocks are marked, and reclaim
// on open removes them.
type stateFetch struct {
	root cid.Cid
	busy chan struct{}

	// Guarded by store.fetchMu.
	links    []link
	heir     *stateFetch
	users    int
	ended    bool
	progress time.Duration
	complete bool

	// Guarded by busy while f has users, and owned by whoever ends it once
	// it has none.
	held []cid.Cid
	seen map[cid.Cid]struct{}
	want []cid.Cid
}

func (f *stateFetch) lock(ctx context.Context) error {
	select {
	case f.busy <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *stateFetch) unlock() { <-f.busy }

// newest is the highest counter among f's snapshots of replica r.
func (f *stateFetch) newest(r string) (uint64, bool) {
	var n uint64
	ok := false
	for _, l := range f.links {
		if l.dot.replica == r && (!ok || l.dot.counter > n) {
			n, ok = l.dot.counter, true
		}
	}
	return n, ok
}

// beginFetch returns the fetch of root for the snapshot l, creating it if
// none is in progress, with the caller counted as one of its users until
// leaveFetch. The fetch of an author's newest snapshot supersedes the
// fetches of its older ones.
func (store *Datastore) beginFetch(root cid.Cid, l link) *stateFetch {
	store.fetchMu.Lock()
	defer store.fetchMu.Unlock()
	f, ok := store.fetches[root]
	if !ok {
		f = &stateFetch{root: root, busy: make(chan struct{}, 1), progress: store.times.up()}
		store.fetches[root] = f
	}
	f.users++
	known := false
	for _, k := range f.links {
		known = known || k.dot == l.dot
	}
	if !known {
		f.links = append(f.links, l)
	}
	store.inherit(l.dot.replica)
	return f
}

// inherit makes the fetch of replica r's newest snapshot the heir of every
// fetch of an older one. The caller holds fetchMu.
func (store *Datastore) inherit(r string) {
	var heir *stateFetch
	var top uint64
	for _, g := range store.fetches {
		if n, ok := g.newest(r); ok && (heir == nil || n > top) {
			heir, top = g, n
		}
	}
	for _, g := range store.fetches {
		if n, ok := g.newest(r); ok && n < top {
			g.heir = heir
		}
	}
}

// leaveFetch ends the caller's use of f: with merged, its closure is
// owned now and f is dropped. The last user of a dropped fetch releases
// what it staged.
func (store *Datastore) leaveFetch(f *stateFetch, merged bool) {
	store.fetchMu.Lock()
	if merged {
		store.dropFetchLocked(f)
	}
	f.users--
	release := f.ended && f.users == 0
	store.fetchMu.Unlock()
	if release {
		store.releaseFetch(f)
	}
}

// dropFetchLocked takes f out of the fetches in progress. The caller holds
// fetchMu.
func (store *Datastore) dropFetchLocked(f *stateFetch) {
	if store.fetches[f.root] == f {
		delete(store.fetches, f.root)
	}
	f.ended = true
}

func (store *Datastore) releaseFetch(f *stateFetch) {
	held := f.held
	f.held, f.seen, f.want = nil, nil, nil
	store.unstage(held)
}

// sweepFetches drops every fetch no attempt is using that will never be
// merged, is superseded, or has fetched nothing for D.
func (store *Datastore) sweepFetches(dropClock uint64) {
	now := store.times.up()
	store.fetchMu.Lock()
	var dead []*stateFetch
	for _, f := range store.fetches {
		if f.users == 0 && store.fetchDead(f, now, dropClock) {
			dead = append(dead, f)
		}
	}
	for _, f := range dead {
		store.dropFetchLocked(f)
	}
	store.fetchMu.Unlock()
	for _, f := range dead {
		store.releaseFetch(f)
	}
}

// fetchDead reports whether f is to be dropped. The caller holds fetchMu.
func (store *Datastore) fetchDead(f *stateFetch, now time.Duration, dropClock uint64) bool {
	wanted := false
	for _, l := range f.links {
		wanted = wanted || !store.covered(l.dot) && !store.gone(l, dropClock)
	}
	if !wanted {
		return true
	}
	if h := f.heir; h != nil && store.fetches[h.root] == h {
		return h.complete
	}
	return now-f.progress > store.opts.DeliveryBound
}

// fetchState fetches every block of a snapshot's state this replica does
// not own, level by level, so a peer that shares most of it with this
// replica sends only the nodes on the paths that changed. Every block of
// the state is staged before it is fetched, the ones not owned marked for
// removal, until the merge acquires them or reclaim removes them once the
// fetch is dropped. An attempt that runs out of time keeps what it fetched
// for the next one. Unless the state's cid is malformed, it returns the
// fetch with the caller as one of its users: the caller leaves it, merged
// once a merge owns the state.
func (store *Datastore) fetchState(ctx context.Context, ng *crdtNodeGetter, c cid.Cid, root *pb.Delta) (*stateFetch, error) {
	stc, err := cid.Cast(root.GetState())
	if err != nil {
		return nil, fmt.Errorf("no state: %w", err)
	}
	f := store.beginFetch(stc, linkOf(c, root))
	store.sweepFetches(store.dropClockNow())
	cctx, cancel := context.WithTimeout(ctx, store.opts.DAGSyncerTimeout)
	defer cancel()
	if err := f.lock(cctx); err != nil {
		return f, err
	}
	defer f.unlock()
	if f.seen == nil {
		f.seen = map[cid.Cid]struct{}{}
		if err := store.discover(ctx, f, []cid.Cid{stc}); err != nil {
			return f, err
		}
	}
	for len(f.want) > 0 {
		var next []cid.Cid
		got := make(map[cid.Cid]struct{}, len(f.want))
		var ferr error
		for opt := range ng.GetMany(cctx, f.want) {
			if opt.Err != nil {
				ferr = errors.Join(ferr, opt.Err)
				continue
			}
			got[opt.Node.Cid()] = struct{}{}
			for _, l := range opt.Node.Links() {
				next = append(next, l.Cid)
			}
		}
		if len(got) > 0 {
			store.fetchMu.Lock()
			f.progress = store.times.up()
			store.fetchMu.Unlock()
		}
		// What the fetched blocks link is staged before they leave the
		// wanted ones, even when the attempt stops here: the next one goes
		// on from it.
		if err := store.discover(ctx, f, next); err != nil {
			return f, errors.Join(ferr, err)
		}
		rest := f.want[:0]
		for _, w := range f.want {
			if _, ok := got[w]; !ok {
				rest = append(rest, w)
			}
		}
		clear(f.want[len(rest):])
		f.want = rest
		// A level that fetched something goes on, through what is held
		// here, until one fetches nothing.
		if len(got) == 0 {
			return f, errors.Join(ferr, fmt.Errorf("%d state blocks not fetched yet", len(rest)))
		}
	}
	store.fetchMu.Lock()
	f.complete = true
	store.fetchMu.Unlock()
	return f, nil
}

// discover stages the blocks of f's closure among cids it has not seen:
// the ones owned, whose closure is held, as they are, and the others
// marked, to be fetched. A block it could not stage is not seen yet.
func (store *Datastore) discover(ctx context.Context, f *stateFetch, cids []cid.Cid) error {
	var owned, want []cid.Cid
	here := make(map[cid.Cid]struct{}, len(cids))
	for _, c := range cids {
		if _, ok := f.seen[c]; ok {
			continue
		}
		if _, ok := here[c]; ok {
			continue
		}
		here[c] = struct{}{}
		has, err := store.store.Has(ctx, store.ownedKey(c))
		if err != nil {
			return err
		}
		if has {
			owned = append(owned, c)
		} else {
			want = append(want, c)
		}
	}
	// stage counts every block before it marks any: f holds them all
	// either way.
	f.held = append(f.held, owned...)
	if err := store.stage(ctx, owned, false); err != nil {
		return err
	}
	for _, c := range owned {
		f.seen[c] = struct{}{}
	}
	f.held = append(f.held, want...)
	if err := store.stage(ctx, want, true); err != nil {
		return err
	}
	for _, c := range want {
		f.seen[c] = struct{}{}
	}
	f.want = append(f.want, want...)
	return nil
}
