package kvdb

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"github.com/taubyte/tau/pkg/kvdb/statetree"
)

// A stateFetch is the fetch of one state's closure, kept across attempts.
// An attempt is bounded by DAGSyncerTimeout, and a state larger than one
// attempt can fetch arrives over several: what an attempt fetched stays the
// fetch's, and the next goes on from the blocks it was still waiting for.
// Nothing of it is visible until a merge publishes the closure.
//
// A fetch is a session of the collector (see gc.go): it reuses every
// subtree the roots already reach, and records every other block it asks
// for as an intent before it asks. It is dropped, and what it fetched that
// no root reaches removed, once none of the snapshots it was begun for can
// be merged any more (covered or gone), once a newer snapshot of the same
// author has fetched its whole closure (superseded), or once it has fetched
// nothing for D: D bounds how long what a replica in contact holds takes to
// reach this one, so a fetch that has received nothing in D has no source
// in contact. A superseded fetch is kept until its successor's walk is
// done. A restart drops every fetch: open removes their intents.
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
	// it has none. Nil until its first attempt.
	b *fetchBuffers
}

// fetchBuffers is what a state fetch holds and works in, reused from one
// fetch to the next: receiving a block allocates nothing once they have
// grown to the size of the states fetched.
type fetchBuffers struct {
	// sess holds every block of the closure the fetch has seen, and
	// want[next:] are the ones not received yet. unstaged are blocks of
	// the closure a discover could not record, the next one's to take
	// first.
	sess     *gcSession
	want     []cid.Cid
	next     int
	unstaged []cid.Cid

	// Scratch for one attempt, events empty between attempts.
	events                        chan fetchEvent
	asked                         map[cidKey]struct{}
	failed, parents, linked, kept []cid.Cid
	fresh                         []cid.Cid
}

var fetchBufferPool = sync.Pool{New: func() any {
	return &fetchBuffers{
		sess:   newSession(),
		asked:  map[cidKey]struct{}{},
		events: make(chan fetchEvent, wantWindow),
	}
}}

func takeFetchBuffers() *fetchBuffers {
	return fetchBufferPool.Get().(*fetchBuffers)
}

func (b *fetchBuffers) wanted() []cid.Cid { return b.want[b.next:] }

// putBack returns b to the pool, keeping its storage and none of the cids
// it held.
func (b *fetchBuffers) putBack() {
	for _, s := range []*[]cid.Cid{&b.want, &b.unstaged, &b.failed, &b.parents, &b.linked, &b.kept, &b.fresh} {
		clear((*s)[:cap(*s)])
		*s = (*s)[:0]
	}
	b.sess.reset()
	clear(b.asked)
	b.next = 0
	fetchBufferPool.Put(b)
}

// A cidKey is a block's identity as a fixed-size value: the digest of a
// block's cid (statetree.BlockPrefix), every block a state holds, or the sha2-256
// of any other cid's bytes. A set of them holds no pointers.
type cidKey [32]byte

func keyOf(c cid.Cid) cidKey {
	ks := c.KeyString()
	if len(ks) == len(statetree.BlockPrefix)+32 && ks[:len(statetree.BlockPrefix)] == statetree.BlockPrefix {
		var k cidKey
		copy(k[:], ks[len(statetree.BlockPrefix):])
		return k
	}
	return sha256.Sum256([]byte(ks))
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

// leaveFetch ends the caller's use of f: with merged, a merge has settled
// the snapshot and f is dropped. The last user of a dropped fetch ends its
// session.
func (store *Datastore) leaveFetch(ctx context.Context, f *stateFetch, merged bool) {
	store.fetchMu.Lock()
	if merged {
		store.dropFetchLocked(f)
	}
	f.users--
	release := f.ended && f.users == 0
	store.fetchMu.Unlock()
	if release {
		store.releaseFetch(ctx, f)
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

func (store *Datastore) releaseFetch(ctx context.Context, f *stateFetch) {
	b := f.b
	if b == nil {
		return
	}
	f.b = nil
	if err := store.endSession(ctx, b.sess); err != nil {
		store.logger.Warnf("error removing what a dropped state fetch fetched: %s", err)
	}
	b.putBack()
}

// sweepFetches drops every fetch no attempt is using that will never be
// merged, is superseded, or has fetched nothing for D.
func (store *Datastore) sweepFetches(ctx context.Context, dropClock uint64) {
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
		store.releaseFetch(ctx, f)
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

// fetchState fetches every block of a snapshot's state the roots do not
// reach, so a peer that shares most of it with this replica sends only the
// nodes on the paths that changed. Every block is an intent of the fetch
// before it is fetched, until the merge publishes it or the fetch is
// dropped. An attempt that runs out of time keeps what it fetched for the
// next one. Unless the state's cid, or what the snapshot carries of the
// state (see carried), is malformed, it returns the fetch with the caller
// as one of its users: the caller leaves it, merged once a merge has
// settled the snapshot.
func (store *Datastore) fetchState(ctx context.Context, ng *crdtNodeGetter, c cid.Cid, root *pb.Delta, held *heldDAG) (*stateFetch, error) {
	stc, err := cid.Cast(root.GetState())
	if err != nil {
		return nil, fmt.Errorf("no state: %w", err)
	}
	cs := takeCarried()
	defer cs.putBack()
	if err := cs.verify(stc, root); err != nil {
		return nil, fmt.Errorf("state %s: %w", stc, err)
	}
	f := store.beginFetch(stc, linkOf(c, root))
	store.sweepFetches(ctx, store.dropClockNow())
	cctx, cancel := context.WithTimeout(ctx, store.opts.DAGSyncerTimeout)
	defer cancel()
	if err := f.lock(cctx); err != nil {
		return f, err
	}
	defer f.unlock()
	if f.b == nil {
		if err := store.seed(ctx, f, cs, held); err != nil {
			return f, err
		}
	}
	if err := store.fetchWanted(ctx, cctx, ng, f, held); err != nil {
		return f, err
	}
	store.fetchMu.Lock()
	f.complete = true
	store.fetchMu.Unlock()
	return f, nil
}

// discover takes the blocks of f's closure among cids, and those an
// earlier discover could not, that it does not hold yet: a subtree the
// roots reach is reused, and every other block recorded as an intent and
// wanted. What it cannot record stays f's to take next time, so a failure
// leaves no block of the closure neither wanted nor held.
func (store *Datastore) discover(ctx context.Context, f *stateFetch, cids []cid.Cid) error {
	b := f.b
	b.unstaged = append(b.unstaged, cids...)
	fresh, err := store.intend(ctx, b.sess, b.unstaged, b.fresh[:0])
	b.fresh = fresh
	b.requeue(b.fresh)
	if err != nil {
		return err
	}
	clear(b.unstaged)
	b.unstaged = b.unstaged[:0]
	return nil
}

// wantWindow bounds how many blocks one fetch has asked for and not yet
// received. A bitswap server keeps at most 1024 wants from a peer and drops
// the rest unanswered, and the client asks again only when it rebroadcasts:
// a fetch that asked for a whole level of a large state at once received
// 1024 blocks and waited out its attempt for the others. The window leaves
// room under that bound for the other fetches asking the same peer.
const wantWindow = 512

// wantBatch is the fewest blocks a fetch asks for at once while more are
// wanted than that: bitswap sends a peer one message of wants every 20ms
// however many gets they come from, and each get costs a session want and
// its goroutines.
const wantBatch = wantWindow / 8

// A fetchEvent is a block a fetch received, a get that failed, or, with
// asked set, the end of one get of many.
type fetchEvent struct {
	node  ipld.Node
	err   error
	asked []cid.Cid
}

// fetchWanted fetches f's wanted blocks, at most wantWindow at a time, and
// asks for what each links as soon as it arrives rather than once its whole
// level has. What a block links is staged before it is asked for, so an
// attempt that stops anywhere leaves every block it has not received
// wanted or still to stage, for the next one to go on from. A block that
// could not be fetched is asked for again while others still arrive; the
// attempt ends once none does. Gets run under cctx, staging under ctx.
func (store *Datastore) fetchWanted(ctx, cctx context.Context, ng ipld.NodeGetter, f *stateFetch, held *heldDAG) error {
	b := f.b
	if len(b.unstaged) > 0 {
		if err := store.discover(ctx, f, nil); err != nil {
			return err
		}
	}
	gctx, stop := context.WithCancel(cctx)
	events := b.events
	gets, arrived := 0, false
	var errs error
	ended := func(batch []cid.Cid) {
		gets--
		for _, c := range batch {
			if k := keyOf(c); hasKey(b.asked, k) {
				delete(b.asked, k)
				b.failed = append(b.failed, c)
			}
		}
	}
	defer func() {
		stop()
		for gets > 0 {
			switch ev := <-events; {
			case ev.asked != nil:
				ended(ev.asked)
			case ev.err == nil && b.received(ev.node):
				held.keep(ev.node)
			}
		}
		// What arrived once the attempt stopped is the next one's to stage.
		b.unstaged = append(b.unstaged, b.linked...)
		clear(b.linked)
		b.linked = b.linked[:0]
		clear(b.parents)
		b.parents = b.parents[:0]
		b.settle()
		b.requeue(b.failed)
		b.failed = b.failed[:0]
	}()
	handle := func(ev fetchEvent) {
		switch {
		case ev.asked != nil:
			ended(ev.asked)
		case ev.err != nil:
			errs = errors.Join(errs, ev.err)
		case b.received(ev.node):
			held.keep(ev.node)
			arrived = true
		}
	}
	for {
		want := b.wanted()
		if n := min(wantWindow-len(b.asked), len(want)); n > 0 && (n == len(want) || n >= wantBatch) && gctx.Err() == nil {
			// The get reads its batch where it lies in the wanted queue,
			// which moves only once the attempt's gets are over.
			batch := want[:n:n]
			b.next += n
			for _, c := range batch {
				b.asked[keyOf(c)] = struct{}{}
			}
			gets++
			go func() {
				for opt := range ng.GetMany(gctx, batch) {
					events <- fetchEvent{node: opt.Node, err: opt.Err}
				}
				events <- fetchEvent{asked: batch}
			}()
		}
		if gets == 0 {
			if len(b.wanted()) == 0 && len(b.failed) == 0 {
				return nil
			}
			if !arrived || gctx.Err() != nil {
				return errors.Join(errs, fmt.Errorf("%d state blocks not fetched yet", len(b.wanted())+len(b.failed)))
			}
			b.settle()
			b.requeue(b.failed)
			b.failed, arrived = b.failed[:0], false
			continue
		}
		handle(<-events)
		for more := true; more; {
			select {
			case ev := <-events:
				handle(ev)
			default:
				more = false
			}
		}
		if len(b.parents) == 0 {
			continue
		}
		store.fetchMu.Lock()
		f.progress = store.times.up()
		store.fetchMu.Unlock()
		err := store.discover(ctx, f, b.linked)
		clear(b.linked)
		b.linked = b.linked[:0]
		clear(b.parents)
		b.parents = b.parents[:0]
		if err != nil {
			return errors.Join(errs, err)
		}
	}
}

// received takes nd, if it was asked for, as a parent whose links are to
// be staged.
func (b *fetchBuffers) received(nd ipld.Node) bool {
	c := nd.Cid()
	k := keyOf(c)
	if !hasKey(b.asked, k) {
		return false
	}
	delete(b.asked, k)
	b.parents = append(b.parents, c)
	// A block whose links do not decode stages none, and the merge refuses
	// it when it reads it.
	if linked, err := blockLinks(nd, b.linked); err == nil {
		b.linked = linked
	}
	return true
}

// requeue wants cids again, after those wanted now.
func (b *fetchBuffers) requeue(cids []cid.Cid) {
	b.want = append(b.want, cids...)
	clear(cids)
}

// settle moves the blocks still wanted to the front of the queue, once no
// get reads it.
func (b *fetchBuffers) settle() {
	n := copy(b.want, b.want[b.next:])
	clear(b.want[n:])
	b.want, b.next = b.want[:n], 0
}

func hasKey(m map[cidKey]struct{}, k cidKey) bool {
	_, ok := m[k]
	return ok
}
