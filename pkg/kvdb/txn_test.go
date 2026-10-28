package kvdb

import (
	"context"
	"fmt"
	"math/bits"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"

	ds "github.com/ipfs/go-datastore"
)

// A scan sees the store under the pending writes, deletes included, in key
// order, and nothing outside its prefix.
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

// A merge settles every key it touched with one scan of that key's
// elements, writing as it goes. Each scan visits about a binary search of
// the pending writes, so settling n keys costs n log n, not n squared: a
// cold 100k-key catch-up once ran 25 minutes walking its own writes.
func TestTxnScanWorkStaysLogarithmic(t *testing.T) {
	ctx := context.Background()
	const n = 4096
	s := &set{namespace: ds.NewKey("/s")}
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
