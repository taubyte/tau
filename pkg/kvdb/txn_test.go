package kvdb

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"

	ds "github.com/ipfs/go-datastore"
	"github.com/taubyte/tau/pkg/pebbleds"
)

func TestTxnScanMatchesPendingWrites(t *testing.T) {
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(1, 2))
	key := func() string {
		return fmt.Sprintf("/n/%c/%c/%d", 'a'+rune(rng.IntN(3)), 'a'+rune(rng.IntN(3)), rng.IntN(20))
	}
	store := ds.NewMapDatastore()
	held := map[string][]byte{}
	for range 200 {
		k := key()
		held[k] = []byte(k)
		if err := store.Put(ctx, ds.NewKey(k), []byte(k)); err != nil {
			t.Fatal(err)
		}
	}
	tx := newTxn(store)
	for i := range 2000 {
		k := ds.NewKey(key())
		if rng.IntN(3) == 0 {
			tx.del(k)
			delete(held, k.String())
		} else {
			v := []byte(fmt.Sprint(i))
			tx.put(k, v)
			held[k.String()] = v
		}
		if i%50 != 0 {
			continue
		}
		for _, p := range []string{"/n", "/n/a", "/n/b/c", "/n/c/a/1", "/n/a/b/1", "/m"} {
			got, err := tx.scan(ctx, ds.NewKey(p))
			if err != nil {
				t.Fatal(err)
			}
			var want []entry
			for k, v := range held {
				if strings.HasPrefix(k, p+"/") {
					want = append(want, entry{key: k, value: v})
				}
			}
			sort.Slice(want, func(i, j int) bool { return want[i].key < want[j].key })
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("after %d writes, scan %s:\n got  %v\n want %v", i+1, p, got, want)
			}
		}
	}
}

func TestTxnScanWorkStaysLogarithmic(t *testing.T) {
	ctx := context.Background()
	const n = 4096
	s := newSet(nil, ds.NewKey("/s"), new(internTable))
	tx := newTxn(ds.NewMapDatastore())
	for i := range n {
		tx.put(s.elemKey(fmt.Sprint(i), dot{replica: "r", counter: uint64(i + 1)}), []byte{})
	}
	for i := range n {
		key := fmt.Sprint(i)
		got, err := tx.scan(ctx, s.keyElemsPrefix(key))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("key %s: %d elements, want 1", key, len(got))
		}
		tx.put(s.valueKey(key), []byte(key))
		tx.put(s.priorityKey(key), []byte{})
	}
	if limit := 8 * n * bits.Len(uint(len(tx.order))); tx.steps > limit {
		t.Fatalf("%d scans over %d writes visited %d entries, want at most %d", n, len(tx.order), tx.steps, limit)
	}
}

func TestCmpAfterIsCompareWithTheByte(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	word := func() string {
		b := make([]byte, rng.IntN(5))
		for i := range b {
			b[i] = "/0a."[rng.IntN(4)]
		}
		return string(b)
	}
	for range 20000 {
		k, p := word(), word()
		for _, c := range []byte{'/', '0'} {
			if got, want := cmpAfter(k, p, c), strings.Compare(k, p+string(c)); got != want {
				t.Fatalf("cmpAfter(%q, %q, %q) = %d, want %d", k, p, c, got, want)
			}
		}
	}
}

func TestAReleasedTxnIsEmpty(t *testing.T) {
	ctx := context.Background()
	store := ds.NewMapDatastore()
	for range 3 {
		tx := newTxn(store)
		if len(tx.writes) != 0 || len(tx.order) != 0 || tx.sorted.root != -1 || len(tx.sorted.left) != 0 {
			t.Fatal("a new txn holds writes")
		}
		got, err := tx.scan(ctx, ds.NewKey("/a"))
		if err != nil || len(got) != 0 {
			t.Fatalf("a new txn scans %v, %v", got, err)
		}
		tx.put(ds.NewKey("/a/b"), []byte("x"))
		tx.del(ds.NewKey("/a/c"))
		tx.release()
	}
	big := newTxn(store)
	for i := range reusedWrites + 1 {
		big.put(ds.NewKey(fmt.Sprint("/a/", i)), nil)
	}
	big.release()
	if len(big.order) != reusedWrites+1 {
		t.Fatal("a txn too large to keep was cleared for reuse")
	}
}

func TestATxnReadsThroughOneReader(t *testing.T) {
	ctx := context.Background()
	store, err := pebbleds.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, k := range []string{"/a/1", "/a/2"} {
		if err := store.Put(ctx, ds.NewKey(k), []byte(k)); err != nil {
			t.Fatal(err)
		}
	}
	tx := newTxn(store)
	tx.put(ds.NewKey("/a/3"), []byte("/a/3"))
	if v, err := tx.get(ctx, ds.NewKey("/a/1")); err != nil || string(v) != "/a/1" {
		t.Fatalf("get: %q %v", v, err)
	}
	rd := tx.rd
	if rd == nil {
		t.Fatal("a read made no reader")
	}
	if err := store.Delete(ctx, ds.NewKey("/a/2")); err != nil {
		t.Fatal(err)
	}
	if ok, err := tx.has(ctx, ds.NewKey("/a/2")); err != nil || !ok {
		t.Fatalf("has: %v %v, want the store as it was at the first read", ok, err)
	}
	got, err := tx.scan(ctx, ds.NewKey("/a"))
	if err != nil || fmt.Sprint(got) != fmt.Sprint([]entry{{"/a/1", []byte("/a/1")}, {"/a/2", []byte("/a/2")}, {"/a/3", []byte("/a/3")}}) {
		t.Fatalf("scan: %v %v", got, err)
	}
	if tx.rd != rd {
		t.Fatal("a txn made a second reader")
	}
	if err := tx.commit(ctx); err != nil {
		t.Fatal(err)
	}
	if tx.rd != nil {
		t.Fatal("commit left the reader open")
	}
	if ok, err := tx.has(ctx, ds.NewKey("/a/2")); err != nil || ok {
		t.Fatalf("has after commit: %v %v, want the store as it is now", ok, err)
	}
	tx.release()
	if tx.rd != nil {
		t.Fatal("release left the reader open")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	tx = newTxn(store)
	defer tx.release()
	if _, err := tx.get(ctx, ds.NewKey("/a/1")); !errors.Is(err, pebbleds.ErrClosed) {
		t.Fatalf("get of a closed store: %v", err)
	}
	if _, err := tx.has(ctx, ds.NewKey("/a/1")); !errors.Is(err, pebbleds.ErrClosed) {
		t.Fatalf("has of a closed store: %v", err)
	}
	if _, err := tx.scan(ctx, ds.NewKey("/a")); !errors.Is(err, pebbleds.ErrClosed) {
		t.Fatalf("scan of a closed store: %v", err)
	}
}
