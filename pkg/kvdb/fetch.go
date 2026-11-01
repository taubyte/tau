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

// A stateFetch fetches one state's closure across attempts, each bounded by
// DAGSyncerTimeout; an attempt goes on from what the last was waiting for, and
// nothing is visible until a merge publishes the closure. It is a gc session:
// it reuses what the roots reach and records every other block as an intent
// before asking. It is dropped, with what it fetched that no root reaches,
// once none of its snapshots can be merged, once a newer snapshot of the same
// author has fetched its whole closure (kept until that walk is done), or once
// it has fetched nothing for D, when no source is in contact. A restart drops
// every fetch.
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

	// Guarded by busy while f has users, then owned by whoever ends it; nil until
	// its first attempt.
	b *fetchBuffers
}

// fetchBuffers are reused from one fetch to the next, so receiving a block
// allocates nothing once they have grown.
type fetchBuffers struct {
	// sess holds every closure block the fetch has seen; want[next:] are not
	// received yet; unstaged are blocks a discover could not record, taken first
	// next time.
	sess     *gcSession
	want     []cid.Cid
	next     int
	unstaged []cid.Cid

	// Scratch for one attempt; events is empty between attempts.
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

// putBack keeps b's storage and none of its cids.
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

// A cidKey is a block's identity as a fixed-size value: a state block's
// digest, or the sha2-256 of any other cid's bytes, so sets of them hold no
// pointers.
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

// The caller is a user of the returned fetch until leaveFetch. The fetch of an
// author's newest snapshot supersedes those of its older ones.
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

// inherit makes the fetch of r's newest snapshot the heir of its older ones.
// The caller holds fetchMu.
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

// With merged, a merge settled the snapshot and f is dropped. The last user of
// a dropped fetch ends its session.
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

// The caller holds fetchMu.
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

// The caller holds fetchMu.
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

// fetchState fetches the state blocks the roots do not reach, so a peer
// sharing most of it sends only the changed paths. An attempt that runs out of
// time keeps what it fetched. Unless the state's cid or what the snapshot
// carries is malformed, the caller is a user of the returned fetch and leaves
// it, merged once a merge settled the snapshot.
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

// discover reuses subtrees the roots reach and records every other block as a
// wanted intent. What it cannot record stays f's to take next time, so a
// failure leaves no closure block neither wanted nor held.
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

// A bitswap server keeps at most 1024 wants from a peer and drops the rest
// unanswered until the client rebroadcasts; the window leaves room under that
// for other fetches asking the same peer.
const wantWindow = 512

// bitswap sends a peer one message of wants every 20ms however many gets they
// come from, and each get costs a session want and goroutines.
const wantBatch = wantWindow / 8

// A fetchEvent is a block received, a failed get, or with asked the end of one
// get of many.
type fetchEvent struct {
	node  ipld.Node
	err   error
	asked []cid.Cid
}

// fetchWanted asks for what each block links as soon as it arrives, at most
// wantWindow at a time. Links are staged before they are asked for, so an
// attempt stopping anywhere leaves every unreceived block wanted or still to
// stage. Gets run under cctx, staging under ctx.
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

func (b *fetchBuffers) requeue(cids []cid.Cid) {
	b.want = append(b.want, cids...)
	clear(cids)
}

// settle runs once no get reads the queue.
func (b *fetchBuffers) settle() {
	n := copy(b.want, b.want[b.next:])
	clear(b.want[n:])
	b.want, b.next = b.want[:n], 0
}

func hasKey(m map[cidKey]struct{}, k cidKey) bool {
	_, ok := m[k]
	return ok
}
