package helpers

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
)

// Goroutines keep using the store while it closes, as a stopping service's do.
// An unguarded pebble datastore panics here and takes the process with it.
func TestDatastoreUsedDuringClose(t *testing.T) {
	store, err := NewDatastore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := datastore.NewKey("/k")
	if err := store.Put(ctx, key, []byte("v")); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 2000 {
				if results, err := store.Query(ctx, query.Query{}); err == nil {
					results.Rest()
					results.Close()
				}
				store.Get(ctx, key)
				store.Has(ctx, key)
				store.Put(ctx, key, []byte("v"))
				if batch, err := store.Batch(ctx); err == nil {
					batch.Put(ctx, key, []byte("v"))
					batch.Commit(ctx)
				}
			}
		}()
	}
	close(start)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	if _, err := store.Get(ctx, key); !errors.Is(err, ErrDatastoreClosed) {
		t.Fatalf("Get after close: %v", err)
	}
	if _, err := store.Query(ctx, query.Query{}); !errors.Is(err, ErrDatastoreClosed) {
		t.Fatalf("Query after close: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
