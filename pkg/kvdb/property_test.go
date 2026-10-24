package kvdb

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
)

// oracle is every element and every removal ever written, as (key, dot).
// Once every replica has merged every delta, the live set is exactly the
// elements no removal names: nothing removed comes back and nothing live is
// lost, whatever the order, duplication, partitions and compactions were.
type oracle struct {
	elems map[string]struct{}
	tombs map[string]struct{}
}

func elemID(key string, d dot) string { return key + "@" + d.String() }

func (o *oracle) record(t testing.TB, r *Datastore) {
	t.Helper()
	_, delta, err := (&crdtNodeGetter{r.dagService}).GetDelta(context.Background(), onlyHead(t, r))
	if err != nil {
		t.Fatal(err)
	}
	d := dotFromPB(delta.GetDot())
	for _, e := range delta.GetElements() {
		o.elems[elemID(e.GetKey(), d)] = struct{}{}
	}
	for _, e := range delta.GetTombstones() {
		o.tombs[elemID(e.GetKey(), dotFromPB(e.GetDot()))] = struct{}{}
	}
}

func (o *oracle) live() []string {
	var out []string
	for e := range o.elems {
		if _, gone := o.tombs[e]; !gone {
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

func liveIDs(t testing.TB, r *Datastore) []string {
	t.Helper()
	var out []string
	err := scanStore(context.Background(), r.store, r.set.elemsPrefix(), func(k string, v []byte) error {
		el, err := r.set.parseElem(k, v)
		if err != nil {
			return err
		}
		out = append(out, elemID(el.key, el.dot))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// indexedBlocks is every block r holds that a walk could reach: deltas and
// snapshot roots, not chunks.
func indexedBlocks(t testing.TB, r *Datastore) []cid.Cid {
	t.Helper()
	var out []cid.Cid
	prefix := r.blocksPrefix()
	err := scanStore(context.Background(), r.store, prefix, func(k string, v []byte) error {
		kind, _, err := decodeBlock(v)
		if err != nil || kind == kindChunk {
			return err
		}
		c, err := cidOfBlockKey(prefix, k)
		if err != nil {
			return err
		}
		out = append(out, c)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].KeyString() < out[j].KeyString() })
	return out
}

// tryMergeOne merges one block if it can still be fetched: the replica that
// held it may have purged it since.
func tryMergeOne(r *Datastore, c cid.Cid) {
	ctx := context.Background()
	ng := &crdtNodeGetter{NodeGetter: r.dagService}
	nd, d, err := ng.GetDelta(ctx, c)
	if err != nil {
		return
	}
	r.processNode(ctx, ng, d, nd)
}

func TestOrderIndependence(t *testing.T) {
	seeds := 300
	if testing.Short() {
		seeds = 40
	}
	for seed := range seeds {
		t.Run(fmt.Sprint(seed), func(t *testing.T) { runInterleaving(t, int64(seed)) })
	}
}

func runInterleaving(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	n := 2 + rng.Intn(3)
	opts := noAutoCompact()
	opts.MaxBatchDeltaSize = 64 + rng.Intn(256)
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, n, opts)
	defer done()
	o := &oracle{elems: map[string]struct{}{}, tombs: map[string]struct{}{}}
	ctx := context.Background()

	group := func(i int) int { return 0 }
	partitioned := rng.Intn(2) == 0
	if partitioned {
		cut := rng.Intn(n-1) + 1
		group = func(i int) int {
			if i < cut {
				return 0
			}
			return 1
		}
	}
	reach := func(dst int, all bool) {
		var rs []ipld.DAGService
		for i, d := range dagsyncs {
			if i != dst && (all || group(i) == group(dst)) {
				rs = append(rs, d.DAGService)
			}
		}
		dagsyncs[dst].setRemotes(rs...)
	}
	peer := func(i int, all bool) int {
		var cands []int
		for j := range n {
			if j != i && (all || group(j) == group(i)) {
				cands = append(cands, j)
			}
		}
		if len(cands) == 0 {
			return -1
		}
		return cands[rng.Intn(len(cands))]
	}

	var trace []string
	steps := 40 + rng.Intn(60)
	for step := range steps {
		healed := step >= steps/2
		i := rng.Intn(n)
		r := replicas[i]
		switch a := rng.Intn(10); {
		case a < 4:
			k := ds.NewKey(fmt.Sprintf("k%d", rng.Intn(4)))
			if rng.Intn(3) == 0 {
				if has, _ := r.Has(ctx, k); !has {
					continue
				}
				if err := r.Delete(ctx, k); err != nil {
					t.Fatal(err)
				}
				trace = append(trace, fmt.Sprintf("%d del %s", i, k))
			} else {
				v := fmt.Sprintf("v%d-%d", i, step)
				if err := r.Put(ctx, k, []byte(v)); err != nil {
					t.Fatal(err)
				}
				trace = append(trace, fmt.Sprintf("%d put %s=%s", i, k, v))
			}
			o.record(t, r)
		case a < 6:
			j := peer(i, healed)
			if j < 0 {
				continue
			}
			reach(i, healed)
			syncHeadsQuiet(r, replicas[j])
			trace = append(trace, fmt.Sprintf("%d sync from %d", i, j))
		case a < 9:
			// Out of order, partial and duplicated: one block of a peer,
			// without its parents, possibly one already merged.
			j := peer(i, healed)
			if j < 0 {
				continue
			}
			blocks := indexedBlocks(t, replicas[j])
			if len(blocks) == 0 {
				continue
			}
			reach(i, healed)
			c := blocks[rng.Intn(len(blocks))]
			tryMergeOne(r, c)
			trace = append(trace, fmt.Sprintf("%d merge one of %d", i, j))
		default:
			compact(t, r)
			trace = append(trace, fmt.Sprintf("%d compact", i))
		}
	}

	// A fresh replica bootstraps from one replica's state first.
	fresh, fdag, closeFresh := makeNReplicasSeparateStores(t, 1, opts)
	defer closeFresh()
	boot := rng.Intn(n)
	fdag[0].setRemotes(dagsyncs[boot].DAGService)
	syncHeadsQuiet(fresh[0], replicas[boot])

	all := append(append([]*Datastore{}, replicas...), fresh[0])
	allDags := append(append([]*fetchThroughDAGSvc{}, dagsyncs...), fdag[0])
	for _, d := range allDags {
		var rs []ipld.DAGService
		for _, o := range allDags {
			if o != d {
				rs = append(rs, o.DAGService)
			}
		}
		d.setRemotes(rs...)
	}
	var finalErrs []string
	for range 4 {
		for _, dst := range all {
			for _, src := range all {
				if src != dst {
					syncHeadsQuiet(dst, src)
				}
			}
			if err := dst.Repair(ctx); err != nil {
				finalErrs = append(finalErrs, err.Error())
			}
		}
	}

	var names []string
	for i := range n {
		names = append(names, fmt.Sprintf("r%d", i))
	}
	names = append(names, "fresh")
	want := strings.Join(o.live(), "\n")
	for i, r := range all {
		if got := strings.Join(liveIDs(t, r), "\n"); got != want {
			t.Fatalf("seed %d: %s holds\n%s\nwant\n%s\ntrace:\n%s", seed, names[i], got, want, strings.Join(trace, "\n"))
		}
		if p := countEntries(t, r, r.set.pendingPrefix()); p != 0 {
			t.Fatalf("seed %d: %s keeps %d pending removals after everything merged\n%s\ntrace:\n%s", seed, names[i], p, crdtState(t, r), strings.Join(trace, "\n"))
		}
		if m := countEntries(t, r, r.missingPrefix()); m != 0 {
			var diag []string
			scanStore(ctx, r.store, r.missingPrefix(), func(k string, v []byte) error {
				d, _ := parseDotSegment(strings.TrimPrefix(k, r.missingPrefix().String()+"/"))
				c, _ := cid.Cast(v)
				_, md, gerr := (&crdtNodeGetter{r.dagService}).GetDelta(ctx, c)
				line := fmt.Sprintf("missing %s %s (snapshot=%t err=%v parents=%d):", d, c, md.GetSnapshot(), gerr, len(md.GetParents()))
				for _, p := range md.GetParents() {
					pd := dotFromPB(p.GetDot())
					line += fmt.Sprintf(" [parent %s covered-by-r0=%t]", pd, r.covered(pd))
				}
				for j, o := range all {
					line += fmt.Sprintf(" %s(covers=%t holds=%t)", names[j], o.covered(d), allDags[j].holds(c))
				}
				diag = append(diag, line)
				return nil
			})
			t.Fatalf("seed %d: %s still misses %d parents\n%s\nrepair errors: %v\ntrace:\n%s", seed, names[i], m, strings.Join(diag, "\n"), finalErrs, strings.Join(trace, "\n"))
		}
	}
	assertSameState(t, names, all...)
}

func syncHeadsQuiet(dst, src *Datastore) {
	heads, _ := src.heads.List()
	for _, h := range heads {
		dst.handleBlock(context.Background(), h)
	}
}

func countEntries(t testing.TB, r *Datastore, prefix ds.Key) int {
	t.Helper()
	n, err := countPrefix(context.Background(), r.store, prefix)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
