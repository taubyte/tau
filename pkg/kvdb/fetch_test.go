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
		n += len(f.seen) - len(f.want)
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
