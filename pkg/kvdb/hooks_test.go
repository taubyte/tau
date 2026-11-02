package kvdb

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
)

type hookLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *hookLog) record(call string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, call)
}

// One merge calls the hooks of the keys it touched in no set order, and one
// key's calls in order.
func (l *hookLog) take() map[string][]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string][]string{}
	for _, c := range l.calls {
		key := strings.Fields(c)[1]
		out[key] = append(out[key], c)
	}
	l.calls = nil
	return out
}

// A hook fires inside the merge that causes it, so a step's calls are all made
// when it returns; every call must be expected by the end.
func hooked(t *testing.T, n int) ([]*Datastore, []*fetchThroughDAGSvc, []*hookLog) {
	t.Helper()
	logs := make([]*hookLog, n)
	replicas, dagsyncs, done := makeSeparateStoresWith(t, n, func(i int) *Options {
		l := &hookLog{}
		logs[i] = l
		o := noAutoCompact()
		o.PutHook = func(k ds.Key, v []byte) { l.record(fmt.Sprintf("put %s %s", k, v)) }
		o.DeleteHook = func(k ds.Key) { l.record("delete " + k.String()) }
		return o
	})
	t.Cleanup(func() {
		done()
		for i, l := range logs {
			if calls := l.take(); len(calls) > 0 {
				t.Errorf("replica %d made hook calls no step expected: %v", i, calls)
			}
		}
	})
	return replicas, dagsyncs, logs
}

func expect(t *testing.T, step string, l *hookLog, want ...string) {
	t.Helper()
	got := l.take()
	w := (&hookLog{calls: want}).take()
	if !maps.EqualFunc(got, w, slices.Equal[[]string]) {
		t.Errorf("%s: hook calls %v, want %v", step, got, w)
	}
}

func absent(t *testing.T, name string, r *Datastore, k string) {
	t.Helper()
	has, err := r.Has(context.Background(), ds.NewKey(k))
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Errorf("replica %s shows %s", name, k)
	}
}

func TestCRDTHooks(t *testing.T) {
	t.Run("put then delete", func(t *testing.T) {
		replicas, _, logs := hooked(t, 3)
		a, b, c := replicas[0], replicas[1], replicas[2]

		p := put(t, a, "k", "v")
		expect(t, "a puts", logs[0], "put /k v")
		syncReplicaHeads(t, a, b)
		expect(t, "b merges the put", logs[1], "put /k v")

		d := del(t, a, "k")
		expect(t, "a deletes", logs[0], "delete /k")
		syncReplicaHeads(t, a, b)
		expect(t, "b merges the delete", logs[1], "delete /k")
		absent(t, "b", b, "k")

		// c hears the delete first: it files the removal, refuses the put
		// when it arrives, and never shows k.
		mergeOne(t, c, d)
		expect(t, "c merges the delete before the put", logs[2])
		absent(t, "c", c, "k")
		mergeOne(t, c, p)
		expect(t, "c merges the put the delete removed", logs[2])
		absent(t, "c", c, "k")
		assertSameState(t, []string{"a", "b", "c"}, a, b, c)
	})

	t.Run("overwrite", func(t *testing.T) {
		replicas, _, logs := hooked(t, 2)
		a, b := replicas[0], replicas[1]

		put(t, a, "k", "v1")
		syncReplicaHeads(t, a, b)
		expect(t, "a puts", logs[0], "put /k v1")
		expect(t, "b merges the put", logs[1], "put /k v1")

		put(t, a, "k", "v2")
		expect(t, "a overwrites", logs[0], "put /k v2")
		syncReplicaHeads(t, a, b)
		expect(t, "b merges the overwrite", logs[1], "put /k v2")

		// The new version wins at a higher priority with the same bytes:
		// what Get reads did not change.
		put(t, a, "k", "v2")
		expect(t, "a writes the bytes k holds", logs[0])
		syncReplicaHeads(t, a, b)
		expect(t, "b merges a write of the bytes k holds", logs[1])
		assertSameState(t, []string{"a", "b"}, a, b)
	})

	t.Run("concurrent versions", func(t *testing.T) {
		replicas, dagsyncs, logs := hooked(t, 2)
		a, b := replicas[0], replicas[1]

		put(t, a, "k", "a")
		put(t, b, "k", "b")
		expect(t, "a puts", logs[0], "put /k a")
		expect(t, "b puts concurrently", logs[1], "put /k b")

		// Equal priorities: the greater value wins.
		syncReplicaHeads(t, a, b)
		expect(t, "b merges a version that loses to its own", logs[1])
		from(dagsyncs, 0, 1)
		syncReplicaHeads(t, b, a)
		expect(t, "a merges the version that wins", logs[0], "put /k b")

		del(t, a, "k")
		expect(t, "a deletes both versions", logs[0], "delete /k")
		syncReplicaHeads(t, a, b)
		expect(t, "b merges the delete", logs[1], "delete /k")
		absent(t, "b", b, "k")
		assertSameState(t, []string{"a", "b"}, a, b)
	})

	// a and b each write k and delete their own version, never having seen
	// the other's; c holds both versions and merges the two deletes one at a
	// time. The key is present until the second.
	type testCase struct {
		name   string
		first  int
		before []string
	}
	for _, tc := range []testCase{
		{"removing the losing version", 0, nil},
		{"removing the winning version", 1, []string{"put /k a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replicas, dagsyncs, logs := hooked(t, 3)
			a, b, c := replicas[0], replicas[1], replicas[2]
			dagsyncs[2].setRemotes(dagsyncs[0].DAGService, dagsyncs[1].DAGService)

			puts := [2]cid.Cid{put(t, a, "k", "a"), put(t, b, "k", "b")}
			dels := [2]cid.Cid{del(t, a, "k"), del(t, b, "k")}
			expect(t, "a puts and deletes", logs[0], "put /k a", "delete /k")
			expect(t, "b puts and deletes", logs[1], "put /k b", "delete /k")

			for _, p := range puts {
				mergeOne(t, c, p)
			}
			expect(t, "c merges both versions", logs[2], "put /k a", "put /k b")

			mergeOne(t, c, dels[tc.first])
			expect(t, "c merges the first delete", logs[2], tc.before...)
			if has, _ := c.Has(context.Background(), ds.NewKey("k")); !has {
				t.Errorf("c lost k with a version of it left")
			}
			mergeOne(t, c, dels[1-tc.first])
			expect(t, "c merges the second delete", logs[2], "delete /k")
			absent(t, "c", c, "k")
		})
	}

	t.Run("a fresh replica merging a snapshot", func(t *testing.T) {
		replicas, _, logs := hooked(t, 3)
		a, b, f := replicas[0], replicas[1], replicas[2]

		put(t, a, "x", "1")
		put(t, a, "y", "2")
		put(t, a, "z", "3")
		del(t, a, "y")
		put(t, a, "x", "4")
		syncReplicaHeads(t, a, b)
		expect(t, "a writes", logs[0], "put /x 1", "put /y 2", "put /z 3", "delete /y", "put /x 4")
		expect(t, "b merges the history", logs[1], "put /x 4", "put /z 3")

		compact(t, a)
		expect(t, "a compacts", logs[0])
		syncReplicaHeads(t, a, b)
		expect(t, "b merges a snapshot of what it holds", logs[1])

		syncReplicaHeads(t, a, f)
		expect(t, "f merges the snapshot", logs[2], "put /x 4", "put /z 3")
		assertSameState(t, []string{"a", "b", "f"}, a, b, f)
	})

	t.Run("duplicate delivery", func(t *testing.T) {
		replicas, _, logs := hooked(t, 2)
		a, b := replicas[0], replicas[1]

		p := put(t, a, "k", "v")
		syncReplicaHeads(t, a, b)
		expect(t, "a puts", logs[0], "put /k v")
		expect(t, "b merges the put", logs[1], "put /k v")
		syncReplicaHeads(t, a, b)
		mergeOne(t, b, p)
		expect(t, "b hears the put again", logs[1])

		d := del(t, a, "k")
		syncReplicaHeads(t, a, b)
		expect(t, "a deletes", logs[0], "delete /k")
		expect(t, "b merges the delete", logs[1], "delete /k")
		syncReplicaHeads(t, a, b)
		mergeOne(t, b, d)
		mergeOne(t, b, p)
		expect(t, "b hears the delete and the put again", logs[1])
		absent(t, "b", b, "k")
	})
}

func TestPutHookCannotWriteIntoABlock(t *testing.T) {
	replicas, _, done := makeSeparateStoresWith(t, 2, func(i int) *Options {
		o := noAutoCompact()
		if i == 1 {
			o.PutHook = func(k ds.Key, v []byte) {
				for j := range v {
					v[j] = 'X'
				}
			}
		}
		return o
	})
	defer done()
	a, b := replicas[0], replicas[1]
	put(t, a, "k", "hello")
	syncReplicaHeads(t, a, b)
	ctx := context.Background()
	got, err := b.Get(ctx, ds.NewKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("b reads %q", got)
	}
	hs, _ := b.heads.List()
	for _, h := range hs {
		nd, err := b.dagService.Get(ctx, h.Cid)
		if err != nil {
			t.Fatal(err)
		}
		delta, err := extractDelta(nd)
		if err != nil {
			t.Fatal(err)
		}
		if v := string(delta.GetElements()[0].GetValue()); v != "hello" {
			t.Fatalf("the block now holds %q", v)
		}
	}
}
