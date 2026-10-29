package kvdb

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
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
	// it has none. Nil until its first attempt.
	b *fetchBuffers
}

// fetchBuffers is what a state fetch holds and works in, reused from one
// fetch to the next: receiving a block allocates nothing once they have
// grown to the size of the states fetched.
type fetchBuffers struct {
	// held is every block staged for the fetch, seen the same blocks as
	// keys, and want[next:] those not received yet.
	held []cid.Cid
	seen map[cidKey]struct{}
	want []cid.Cid
	next int

	// Scratch for one attempt, events empty between attempts.
	events                        chan fetchEvent
	asked                         map[cidKey]struct{}
	failed, parents, linked, kept []cid.Cid
	owned, fresh                  []cid.Cid
	key                           []byte
}

var fetchBufferPool = sync.Pool{New: func() any {
	return &fetchBuffers{
		seen:   map[cidKey]struct{}{},
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
	for _, s := range []*[]cid.Cid{&b.held, &b.want, &b.failed, &b.parents, &b.linked, &b.kept, &b.owned, &b.fresh} {
		clear((*s)[:cap(*s)])
		*s = (*s)[:0]
	}
	clear(b.seen)
	clear(b.asked)
	b.next = 0
	fetchBufferPool.Put(b)
}

// A cidKey is a block's identity as a fixed-size value: the digest of a
// CIDv1 dag-pb sha2-256 cid, every block a state holds, or the sha2-256 of
// any other cid's bytes. A set of them holds no pointers.
type cidKey [32]byte

// dagPBSHA256 is the prefix of a CIDv1 dag-pb cid with a 32-byte sha2-256
// digest: version, codec, hash function, digest length.
const dagPBSHA256 = "\x01\x70\x12\x20"

func keyOf(c cid.Cid) cidKey {
	ks := c.KeyString()
	if len(ks) == len(dagPBSHA256)+32 && ks[:len(dagPBSHA256)] == dagPBSHA256 {
		var k cidKey
		copy(k[:], ks[len(dagPBSHA256):])
		return k
	}
	return sha256.Sum256([]byte(ks))
}

// ownedKey is store.ownedKey(c) built in b's key buffer, which the key
// shares: it is valid until the next call.
func (b *fetchBuffers) ownedKey(store *Datastore, c cid.Cid) ds.Key {
	b.key = appendHashKey(b.key[:0], store.pre.owned, c)
	return ds.RawKey(unsafe.String(unsafe.SliceData(b.key), len(b.key)))
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
	b := f.b
	if b == nil {
		return
	}
	f.b = nil
	store.unstage(b.held)
	b.putBack()
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
// not own, so a peer that shares most of it with this
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
	if f.b == nil {
		f.b = takeFetchBuffers()
		if err := store.discover(ctx, f, []cid.Cid{stc}); err != nil {
			return f, err
		}
	}
	if err := store.fetchWanted(ctx, cctx, ng, f); err != nil {
		return f, err
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
	b := f.b
	b.owned, b.fresh = b.owned[:0], b.fresh[:0]
	unsee := func(cids []cid.Cid) {
		for _, c := range cids {
			delete(b.seen, keyOf(c))
		}
	}
	for _, c := range cids {
		k := keyOf(c)
		if _, ok := b.seen[k]; ok {
			continue
		}
		b.seen[k] = struct{}{}
		// Has does not keep the key.
		has, err := store.store.Has(ctx, b.ownedKey(store, c))
		if err != nil {
			delete(b.seen, k)
			unsee(b.owned)
			unsee(b.fresh)
			return err
		}
		if has {
			b.owned = append(b.owned, c)
		} else {
			b.fresh = append(b.fresh, c)
		}
	}
	// stage counts every block before it marks any: f holds them all
	// either way.
	b.held = append(b.held, b.owned...)
	if err := store.stage(ctx, b.owned, false); err != nil {
		unsee(b.owned)
		unsee(b.fresh)
		return err
	}
	if len(b.fresh) == 0 {
		return nil
	}
	b.held = append(b.held, b.fresh...)
	if err := store.stage(ctx, b.fresh, true); err != nil {
		unsee(b.fresh)
		return err
	}
	b.requeue(b.fresh)
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
// wanted, for the next one to go on from. A block that could not be fetched
// is asked for again while others still arrive; the attempt ends once none
// does. Gets run under cctx, staging under ctx.
func (store *Datastore) fetchWanted(ctx, cctx context.Context, ng ipld.NodeGetter, f *stateFetch) error {
	b := f.b
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
			if ev := <-events; ev.asked != nil {
				ended(ev.asked)
			}
		}
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
		default:
			arrived = b.received(ev.node) || arrived
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
		// A block leaves the wanted ones once what it links is staged.
		err := store.discover(ctx, f, b.linked)
		clear(b.linked)
		b.linked = b.linked[:0]
		if err != nil {
			b.requeue(b.parents)
			clear(b.parents)
			b.parents = b.parents[:0]
			return errors.Join(errs, err)
		}
		clear(b.parents)
		b.parents = b.parents[:0]
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
	for _, l := range nd.Links() {
		b.linked = append(b.linked, l.Cid)
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
