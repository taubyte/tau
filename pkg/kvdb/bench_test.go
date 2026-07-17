package kvdb

import (
	"context"
	"fmt"
	"testing"

	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
)

// benchReplica returns a single offline-ish replica for micro-benchmarks.
func benchReplica(b *testing.B) *Datastore {
	b.Helper()
	replicas, closeReplicas := makeNReplicas(b, 1, nil)
	b.Cleanup(closeReplicas)
	return replicas[0]
}

// BenchmarkDeleteManyVersions measures deleting a key that has accumulated
// many versions (element markers). This exercises Rmv + putTombs +
// findBestValue.
func BenchmarkDeleteManyVersions(b *testing.B) {
	const versions = 100
	ctx := context.Background()
	r := benchReplica(b)

	keys := make([]ds.Key, b.N)
	for i := range b.N {
		keys[i] = ds.NewKey(fmt.Sprintf("bench-del-%d", i))
		for v := range versions {
			if err := r.Put(ctx, keys[i], []byte(fmt.Sprintf("value-%d", v))); err != nil {
				b.Fatal(err)
			}
		}
	}

	b.ResetTimer()
	for i := range b.N {
		if err := r.Delete(ctx, keys[i]); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkQueryKeysOnly measures a KeysOnly query over many keys with
// sizeable values.
func BenchmarkQueryKeysOnly(b *testing.B) {
	const numKeys = 2000
	ctx := context.Background()
	r := benchReplica(b)

	val := make([]byte, 1024)
	crdtBatch, err := r.Batch(ctx)
	if err != nil {
		b.Fatal(err)
	}
	for i := range numKeys {
		if err := crdtBatch.Put(ctx, ds.NewKey(fmt.Sprintf("bench-q-%d", i)), val); err != nil {
			b.Fatal(err)
		}
	}
	if err := crdtBatch.Commit(ctx); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for range b.N {
		res, err := r.Query(ctx, query.Query{KeysOnly: true})
		if err != nil {
			b.Fatal(err)
		}
		n := 0
		for range res.Next() {
			n++
		}
		res.Close()
		if n != numKeys {
			b.Fatalf("expected %d keys, got %d", numKeys, n)
		}
	}
}

// BenchmarkPut measures single-key put throughput (delta creation, DAG node,
// local merge).
func BenchmarkPut(b *testing.B) {
	ctx := context.Background()
	r := benchReplica(b)
	val := make([]byte, 256)

	b.ResetTimer()
	for i := range b.N {
		if err := r.Put(ctx, ds.NewKey(fmt.Sprintf("bench-put-%d", i)), val); err != nil {
			b.Fatal(err)
		}
	}
}
