package kvdb

import (
	"context"
	"strconv"
	"testing"

	logging "github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/p2p/peer"
)

// kvdb ops benchmarks. They exercise only the public kvdb API (factory New ->
// KVDB Put/Get/Delete/List/Batch) so the same file runs unchanged against the
// pre-vendor baseline (ipfs/go-ds-crdt v0.6.7) and the vendored dag-compaction
// fork, for a like-for-like benchstat comparison.

var benchValue = []byte("benchmark-value-payload-0123456789")

func benchDB(b *testing.B) (*kvDatabase, func()) {
	b.Helper()
	logger := logging.Logger("bench")
	ctx, cancel := context.WithCancel(context.Background())
	node := peer.Mock(ctx)
	f := New(node)
	kv, err := f.New(logger, "bench", 10)
	if err != nil {
		cancel()
		b.Fatalf("new kvdb: %v", err)
	}
	db := kv.(*kvDatabase)
	return db, func() { db.Close(); cancel() }
}

func seed(b *testing.B, db *kvDatabase, n int) {
	b.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if err := db.Put(ctx, "k/"+strconv.Itoa(i), benchValue); err != nil {
			b.Fatalf("seed put: %v", err)
		}
	}
}

// Put: sequential writes of distinct keys.
func BenchmarkKVDB_Put(b *testing.B) {
	db, done := benchDB(b)
	defer done()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.Put(ctx, "k/"+strconv.Itoa(i), benchValue); err != nil {
			b.Fatal(err)
		}
	}
}

// Overwrite: repeated writes to a single key. This grows the CRDT version chain
// on the baseline; the fork's compaction/reclaim collapses it.
func BenchmarkKVDB_Overwrite(b *testing.B) {
	db, done := benchDB(b)
	defer done()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.Put(ctx, "hot", benchValue); err != nil {
			b.Fatal(err)
		}
	}
}

// DeleteManyVersions: delete a key that has accumulated many versions. The
// baseline walks the whole version chain; the fork's compaction keeps it short.
// Each iteration builds a fresh N-version key (untimed) then times the delete.
func BenchmarkKVDB_DeleteManyVersions(b *testing.B) {
	db, done := benchDB(b)
	defer done()
	ctx := context.Background()
	const versions = 100
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		key := "v/" + strconv.Itoa(i)
		for j := 0; j < versions; j++ {
			if err := db.Put(ctx, key, benchValue); err != nil {
				b.Fatal(err)
			}
		}
		b.StartTimer()
		if err := db.Delete(ctx, key); err != nil {
			b.Fatal(err)
		}
	}
}

// KeysOnly: enumerate keys over a large keyspace of 1KiB values. Keys-only
// queries shouldn't pay for values; a bloated DAG makes the baseline walk more.
func BenchmarkKVDB_KeysOnly(b *testing.B) {
	db, done := benchDB(b)
	defer done()
	ctx := context.Background()
	const n = 2000
	oneKiB := make([]byte, 1024)
	for i := 0; i < n; i++ {
		if err := db.Put(ctx, "k/"+strconv.Itoa(i), oneKiB); err != nil {
			b.Fatalf("seed: %v", err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		keys, err := db.List(ctx, "k/")
		if err != nil {
			b.Fatal(err)
		}
		if len(keys) != n {
			b.Fatalf("got %d keys, want %d", len(keys), n)
		}
	}
}

// Get: reads over a pre-seeded keyspace.
func BenchmarkKVDB_Get(b *testing.B) {
	db, done := benchDB(b)
	defer done()
	const n = 1000
	seed(b, db, n)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.Get(ctx, "k/"+strconv.Itoa(i%n)); err != nil {
			b.Fatal(err)
		}
	}
}

// Delete: delete over a pre-seeded keyspace (tombstones).
func BenchmarkKVDB_Delete(b *testing.B) {
	db, done := benchDB(b)
	defer done()
	seed(b, db, b.N)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.Delete(ctx, "k/"+strconv.Itoa(i)); err != nil {
			b.Fatal(err)
		}
	}
}

// List: enumerate a pre-seeded keyspace (walks the merged CRDT set).
func BenchmarkKVDB_List(b *testing.B) {
	db, done := benchDB(b)
	defer done()
	seed(b, db, 1000)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.List(ctx, "k/"); err != nil {
			b.Fatal(err)
		}
	}
}

// Batch: grouped writes committed together.
func BenchmarkKVDB_Batch(b *testing.B) {
	db, done := benchDB(b)
	defer done()
	ctx := context.Background()
	const batchSize = 100
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bt, err := db.Batch(ctx)
		if err != nil {
			b.Fatal(err)
		}
		for j := 0; j < batchSize; j++ {
			if err := bt.Put("b/"+strconv.Itoa(i)+"/"+strconv.Itoa(j), benchValue); err != nil {
				b.Fatal(err)
			}
		}
		if err := bt.Commit(); err != nil {
			b.Fatal(err)
		}
	}
}
