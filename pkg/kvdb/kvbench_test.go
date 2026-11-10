package kvdb

import (
	"context"
	"strconv"
	"testing"

	logging "github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/p2p/peer/peertest"
)

var benchValue = []byte("benchmark-value-payload-0123456789")

func benchDB(b *testing.B) (*kvDatabase, func()) {
	b.Helper()
	logger := logging.Logger("bench")
	ctx, cancel := context.WithCancel(context.Background())
	node := peertest.Mock(b, ctx)
	db, err := opener{node}.New(logger, "bench", 10)
	if err != nil {
		node.Close()
		cancel()
		b.Fatalf("new kvdb: %v", err)
	}
	return db, func() { db.Close(); node.Close(); cancel() }
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
