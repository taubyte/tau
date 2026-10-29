package kvdb

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
)

// rationed serves at most left blocks until refilled: a peer behind a link
// that delivers only so much within one fetch attempt.
type rationed struct {
	ipld.DAGService

	mu     sync.Mutex
	left   int
	served map[cid.Cid]int
}

func newRationed(d ipld.DAGService) *rationed {
	return &rationed{DAGService: d, served: map[cid.Cid]int{}}
}

func (r *rationed) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.left == 0 {
		return nil, ipld.ErrNotFound{Cid: c}
	}
	nd, err := r.DAGService.Get(ctx, c)
	if err == nil {
		r.left--
		r.served[c]++
	}
	return nd, err
}

func (r *rationed) refill(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.left = n
}

func (r *rationed) servedOf(cids []cid.Cid) map[cid.Cid]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[cid.Cid]int{}
	for _, c := range cids {
		if n := r.served[c]; n > 0 {
			out[c] = n
		}
	}
	return out
}

// stateFetched is how many blocks the state fetches in progress have
// fetched.
func (store *Datastore) stateFetched(ctx context.Context) int {
	store.fetchMu.Lock()
	fs := slices.Collect(maps.Values(store.fetches))
	store.fetchMu.Unlock()
	n := 0
	for _, f := range fs {
		if err := f.lock(ctx); err != nil {
			continue
		}
		if f.b != nil {
			n += len(f.b.seen) - len(f.b.wanted())
		}
		f.unlock()
	}
	return n
}

// stateClosure is every block of the state r's only head carries.
func stateClosure(t testing.TB, r *Datastore) []cid.Cid {
	t.Helper()
	ctx := context.Background()
	_, root, err := (&crdtNodeGetter{r.dagService}).GetDelta(ctx, onlyHead(t, r))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[cid.Cid]bool{}
	todo := []cid.Cid{cid.MustParse(root.GetState())}
	for len(todo) > 0 {
		c := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if seen[c] {
			continue
		}
		seen[c] = true
		nd, err := r.dagService.Get(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range nd.Links() {
			todo = append(todo, l.Cid)
		}
	}
	return slices.Collect(maps.Keys(seen))
}

// catchUpInAttempts walks from's heads on r, then repairs, with maintenance
// and so reclaim between attempts, until r misses nothing, the link
// delivering per blocks an attempt. It returns the attempts it took.
func catchUpInAttempts(t testing.TB, r, from *Datastore, link *rationed, per, limit int) int {
	t.Helper()
	n, err := attemptCatchUp(t, r, from, link, per, limit)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// attemptCatchUp is catchUpInAttempts, giving up after limit attempts.
func attemptCatchUp(t testing.TB, r, from *Datastore, link *rationed, per, limit int) (int, error) {
	t.Helper()
	ctx := context.Background()
	link.refill(per)
	hs, _ := from.heads.List()
	for _, h := range hs {
		if err := r.handleBlock(ctx, h); err != nil {
			t.Logf("attempt 1: %v", err)
		}
	}
	n := 1
	for ; r.IsDirty(ctx); n++ {
		if n == limit {
			return n, fmt.Errorf("still missing blocks after %d attempts of %d blocks each", n, per)
		}
		if err := r.maintain(ctx); err != nil {
			return n, err
		}
		link.refill(per)
		if err := r.Repair(ctx); err != nil {
			t.Logf("attempt %d: %v", n+1, err)
		}
	}
	return n, nil
}

func seedKeys(t testing.TB, r *Datastore, n int, prefix string) {
	t.Helper()
	b, err := r.Batch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		if err := b.Put(context.Background(), ds.NewKey(fmt.Sprintf("/%s%04d", prefix, i)), []byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestAStateLargerThanOneAttemptArrives: a state whose closure one fetch
// attempt cannot fetch arrives over several, with maintenance and its
// reclaim between them, and no block of it is fetched twice: each attempt
// goes on from what the last one fetched.
func TestAStateLargerThanOneAttemptArrives(t *testing.T) {
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, noAutoCompact())
	defer done()
	a, b := replicas[0], replicas[1]
	link := newRationed(dagsyncs[0].DAGService)
	dagsyncs[1].setRemotes(link)
	seedKeys(t, a, 400, "k")
	compact(t, a)
	state := stateClosure(t, a)
	per := len(state)/4 + 1
	if len(state) < 8 {
		t.Fatalf("the state has only %d blocks", len(state))
	}

	attempts := catchUpInAttempts(t, b, a, link, per, 4*len(state))
	if attempts < 4 {
		t.Fatalf("the state of %d blocks arrived in %d attempts of %d blocks", len(state), attempts, per)
	}
	if got, want := queryAll(t, b), queryAll(t, a); !sameKV(got, want) {
		t.Fatalf("b holds %d keys, want %d", len(got), len(want))
	}
	for c, n := range link.servedOf(state) {
		if n > 1 {
			t.Errorf("state block %s was fetched %d times", c, n)
		}
	}
	if err := b.maintain(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertTreesMatch(t, b)
	assertCounts(t, b)
	assertNoLeak(t, b, dagsyncs[1])
}

// TestANewerHeadReusesWhatTheOlderOneStaged: the author compacts again
// while a replica is still fetching its last snapshot, and that fetch has
// received nothing for longer than D. The newer snapshot's fetch takes what
// the older one fetched as held, maintenance and reclaim between attempts
// included, so no block is fetched twice. Once the newer one is merged what
// only the older one needed is gone.
func TestANewerHeadReusesWhatTheOlderOneStaged(t *testing.T) {
	ctx := context.Background()
	var up atomic.Int64
	opts := noAutoCompact()
	opts.Uptime = func() time.Duration { return time.Duration(up.Load()) }
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, opts)
	defer done()
	a, b := replicas[0], replicas[1]
	link := newRationed(dagsyncs[0].DAGService)
	dagsyncs[1].setRemotes(link)
	seedKeys(t, a, 400, "k")
	compact(t, a)

	link.refill(len(stateClosure(t, a))/3 + 1)
	syncHeadsLogged(t, a, b)
	if !b.IsDirty(ctx) {
		t.Fatal("the first attempt fetched the whole state")
	}
	put(t, a, "/k0007", "again")
	compact(t, a)
	state := stateClosure(t, a)
	up.Add(int64(b.opts.DeliveryBound) + 1)

	// Each attempt fetches the newer snapshot's block and one block of its
	// state.
	if _, err := attemptCatchUp(t, b, a, link, 2, 4*len(state)); err != nil {
		t.Fatal(err)
	}
	if got, want := queryAll(t, b), queryAll(t, a); !sameKV(got, want) {
		t.Fatalf("b holds %d keys, want %d", len(got), len(want))
	}
	for c, n := range link.servedOf(state) {
		if n > 1 {
			t.Errorf("state block %s was fetched %d times", c, n)
		}
	}
	if err := b.maintain(ctx); err != nil {
		t.Fatal(err)
	}
	assertCounts(t, b)
	assertNoLeak(t, b, dagsyncs[1])
	if n := fetchesInProgress(b); n != 0 {
		t.Fatalf("%d state fetches are still kept", n)
	}
}

func fetchesInProgress(r *Datastore) int {
	r.fetchMu.Lock()
	defer r.fetchMu.Unlock()
	return len(r.fetches)
}

// TestAFetchNothingFeedsIsDropped: a fetch whose source is gone keeps what
// it fetched for D after the last block arrived, then maintenance drops it
// and reclaims what it staged.
func TestAFetchNothingFeedsIsDropped(t *testing.T) {
	ctx := context.Background()
	var up atomic.Int64
	opts := noAutoCompact()
	opts.Uptime = func() time.Duration { return time.Duration(up.Load()) }
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, opts)
	defer done()
	a, b := replicas[0], replicas[1]
	link := newRationed(dagsyncs[0].DAGService)
	dagsyncs[1].setRemotes(link)
	seedKeys(t, a, 400, "k")
	compact(t, a)
	empty := dagsyncs[1].blockCount(t)

	link.refill(len(stateClosure(t, a)) / 2)
	syncHeadsLogged(t, a, b)
	dagsyncs[1].setRemotes()
	fetched := dagsyncs[1].blockCount(t)
	if fetched <= empty {
		t.Fatal("the first attempt fetched nothing")
	}
	up.Add(int64(b.opts.DeliveryBound))
	if err := b.maintain(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Repair(ctx); err == nil {
		t.Fatal("a state nobody serves arrived")
	}
	if fetchesInProgress(b) != 1 || dagsyncs[1].blockCount(t) != fetched {
		t.Fatal("a fetch was dropped before D passed without a block")
	}
	up.Add(1)
	if err := b.maintain(ctx); err != nil {
		t.Fatal(err)
	}
	if n := fetchesInProgress(b); n != 0 {
		t.Fatalf("%d state fetches are kept past D without a block", n)
	}
	if got := dagsyncs[1].blockCount(t); got != empty {
		t.Fatalf("b holds %d blocks, %d before the fetch", got, empty)
	}
	assertCounts(t, b)
	assertNoLeak(t, b, dagsyncs[1])
}

// servingPeer is a replica's DAG service as bitswap makes it: a get of many
// asks for every block at once, the blocks arrive as the peer sends them,
// the peer keeps at most limit of them wanted, dropping the others
// unanswered, and nothing arrives once the get is given up. A block in hold is sent only once a block of release is
// asked for.
type servingPeer struct {
	*fetchThroughDAGSvc
	limit int

	mu       sync.Mutex
	wanted   int
	most     int
	dropped  int
	hold     map[cid.Cid]bool
	release  map[cid.Cid]bool
	released chan struct{}
}

func newServingPeer(limit int) *servingPeer {
	return &servingPeer{fetchThroughDAGSvc: newFetchThrough(), limit: limit, released: make(chan struct{})}
}

func (p *servingPeer) GetMany(ctx context.Context, cids []cid.Cid) <-chan *ipld.NodeOption {
	out := make(chan *ipld.NodeOption, len(cids))
	var wg sync.WaitGroup
	for _, c := range cids {
		local := p.holds(c)
		p.mu.Lock()
		served := local || p.wanted < p.limit
		switch {
		case !served:
			p.dropped++
		case !local:
			p.wanted++
			p.most = max(p.most, p.wanted)
		}
		if p.release[c] && !p.hold[c] {
			select {
			case <-p.released:
			default:
				close(p.released)
			}
		}
		held := p.hold[c]
		p.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !served {
				<-ctx.Done()
				out <- &ipld.NodeOption{Err: ctx.Err()}
				return
			}
			if held {
				select {
				case <-p.released:
				case <-ctx.Done():
				}
			}
			var nd ipld.Node
			err := ctx.Err()
			if err == nil {
				nd, err = p.Get(ctx, c)
			}
			if !local {
				p.mu.Lock()
				p.wanted--
				p.mu.Unlock()
			}
			out <- &ipld.NodeOption{Node: nd, Err: err}
		}()
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

// stateLevels is the state r's only head carries, level by level from its
// root.
func stateLevels(t testing.TB, r *Datastore) [][]ipld.Node {
	t.Helper()
	ctx := context.Background()
	_, root, err := (&crdtNodeGetter{r.dagService}).GetDelta(ctx, onlyHead(t, r))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[cid.Cid]bool{}
	var levels [][]ipld.Node
	for next := []cid.Cid{cid.MustParse(root.GetState())}; len(next) > 0; {
		var level []ipld.Node
		var below []cid.Cid
		for _, c := range next {
			if seen[c] {
				continue
			}
			seen[c] = true
			nd, err := r.dagService.Get(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			level = append(level, nd)
			for _, l := range nd.Links() {
				below = append(below, l.Cid)
			}
		}
		levels = append(levels, level)
		next = below
	}
	return levels
}

// catchUpThrough opens a replica whose DAG service is p, fetching from a.
func catchUpThrough(t *testing.T, a *Datastore, src *fetchThroughDAGSvc, p *servingPeer) *Datastore {
	t.Helper()
	p.setRemotes(src.DAGService)
	o := noAutoCompact()
	o.DAGSyncerTimeout = time.Second
	b, err := openOn(t, newSortedStore(), p, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	syncHeadsLogged(t, a, b)
	return b
}

// TestAFetchWantsNoMoreThanAPeerKeeps: a state with a level wider than
// wantWindow arrives in one attempt from a peer that keeps only wantWindow
// wants: the fetch never has more than that many asked for and unanswered.
// Asking for the whole level at once, as bitswap's 1024-want bound did to
// a 100k-key state, leaves the wants past the bound unanswered until the
// attempt runs out.
func TestAFetchWantsNoMoreThanAPeerKeeps(t *testing.T) {
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 1, noAutoCompact())
	defer done()
	a := replicas[0]
	seedKeys(t, a, 11000, "k")
	compact(t, a)
	widest := 0
	for _, l := range stateLevels(t, a) {
		widest = max(widest, len(l))
	}
	if widest <= wantWindow {
		t.Fatalf("the widest level has %d blocks, not more than %d", widest, wantWindow)
	}

	p := newServingPeer(wantWindow)
	b := catchUpThrough(t, a, dagsyncs[0], p)
	if b.IsDirty(context.Background()) || p.dropped > 0 {
		t.Fatalf("the peer dropped %d wants, at most %d outstanding, and the catch-up did not finish in one attempt", p.dropped, p.most)
	}
	if got, want := queryAll(t, b), queryAll(t, a); !sameKV(got, want) {
		t.Fatalf("b holds %d keys, want %d", len(got), len(want))
	}
	assertCounts(t, b)
	assertNoLeak(t, b, p.fetchThroughDAGSvc)
}

// TestABlockIsAskedForOnceItsParentArrives: a block's children are asked
// for as soon as it arrives, not once its whole level has. The peer holds
// back one block of a level until a child of another block of that level
// is asked for; a fetch that waits for whole levels never asks, and the
// attempt runs out.
func TestABlockIsAskedForOnceItsParentArrives(t *testing.T) {
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 1, noAutoCompact())
	defer done()
	a := replicas[0]
	seedKeys(t, a, 1000, "k")
	compact(t, a)
	var level []ipld.Node
	for _, l := range stateLevels(t, a) {
		if len(l) > 1 && len(l[0].Links()) > 0 && len(l[1].Links()) > 0 {
			level = l
			break
		}
	}
	if level == nil {
		t.Fatal("no level has two blocks with children")
	}

	p := newServingPeer(wantWindow)
	p.hold = map[cid.Cid]bool{level[0].Cid(): true}
	p.release = map[cid.Cid]bool{}
	for _, nd := range level[1:] {
		for _, l := range nd.Links() {
			p.release[l.Cid] = true
		}
	}
	b := catchUpThrough(t, a, dagsyncs[0], p)
	if b.IsDirty(context.Background()) {
		t.Fatal("the catch-up waited for the held block before asking for its siblings' children")
	}
	if got, want := queryAll(t, b), queryAll(t, a); !sameKV(got, want) {
		t.Fatalf("b holds %d keys, want %d", len(got), len(want))
	}
	assertCounts(t, b)
	assertNoLeak(t, b, p.fetchThroughDAGSvc)
}

// TestAFetchSpellsOwnershipAsTheStoreDoes: the ownership key a fetch
// builds in its buffer is the store's, for state blocks and for any other
// cid, and no two cids share a fetch key.
func TestAFetchSpellsOwnershipAsTheStoreDoes(t *testing.T) {
	replicas, _, done := makeNReplicasSeparateStores(t, 1, noAutoCompact())
	defer done()
	a := replicas[0]
	seedKeys(t, a, 50, "k")
	compact(t, a)
	cids := stateClosure(t, a)
	for _, c := range cids[:2] {
		cids = append(cids, cid.NewCidV0(c.Hash()), cid.NewCidV1(cid.Raw, c.Hash()))
	}
	b := a.takeFetchBuffers()
	defer b.putBack()
	keys := map[cidKey]cid.Cid{}
	for _, c := range cids {
		if got, want := b.ownedKey(a, c).String(), a.ownedKey(c).String(); got != want {
			t.Errorf("%s: the fetch spells %s, the store %s", c, got, want)
		}
		if o, ok := keys[keyOf(c)]; ok {
			t.Errorf("%s and %s share a fetch key", o, c)
		}
		keys[keyOf(c)] = c
	}
}

// TestReceivingABlockAllocatesOnlyItsLinks: once a fetch's buffers have
// grown, taking a block it asked for and staging what it links allocates
// nothing but the copy of the links merkledag hands out.
func TestReceivingABlockAllocatesOnlyItsLinks(t *testing.T) {
	ctx := context.Background()
	replicas, _, done := makeNReplicasSeparateStores(t, 1, noAutoCompact())
	defer done()
	a := replicas[0]
	seedKeys(t, a, 1000, "k")
	compact(t, a)
	var nd ipld.Node
	for _, level := range stateLevels(t, a) {
		for _, n := range level {
			if nd == nil || len(n.Links()) > len(nd.Links()) {
				nd = n
			}
		}
	}
	if len(nd.Links()) < 8 {
		t.Fatalf("no block of the state links more than %d", len(nd.Links()))
	}

	f := &stateFetch{b: a.takeFetchBuffers()}
	defer f.b.putBack()
	receive := func() {
		f.b.asked[keyOf(nd.Cid())] = struct{}{}
		if !f.b.received(nd) {
			t.Fatal("a block asked for was not taken")
		}
		if err := a.discover(ctx, f, f.b.linked); err != nil {
			t.Fatal(err)
		}
		a.unstage(f.b.held)
		clear(f.b.seen)
		f.b.held, f.b.linked, f.b.parents = f.b.held[:0], f.b.linked[:0], f.b.parents[:0]
	}
	receive()
	if n := testing.AllocsPerRun(100, receive); n > 1 {
		t.Fatalf("receiving a block of %d links allocates %.1f times", len(nd.Links()), n)
	}
}
