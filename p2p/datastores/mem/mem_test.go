package mem

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	datastore "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
)

func TestDatastore_PutAndGet(t *testing.T) {
	ctx := context.Background()
	ds := New()

	key := datastore.NewKey("testkey")
	value := []byte("testvalue")

	err := ds.Put(ctx, key, value)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	retrievedValue, err := ds.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if !bytes.Equal(value, retrievedValue) {
		t.Fatalf("Expected value %v, got %v", value, retrievedValue)
	}
}

func TestDatastore_Has(t *testing.T) {
	ctx := context.Background()
	ds := New()

	key := datastore.NewKey("testkey")
	value := []byte("testvalue")

	exists, err := ds.Has(ctx, key)
	if err != nil {
		t.Fatalf("Has failed: %v", err)
	}
	if exists {
		t.Fatalf("Should not have key %v", key)
	}

	err = ds.Put(ctx, key, value)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	exists, err = ds.Has(ctx, key)
	if err != nil {
		t.Fatalf("Has failed: %v", err)
	}
	if !exists {
		t.Fatalf("Should have key %v", key)
	}
}

func TestDatastore_Delete(t *testing.T) {
	ctx := context.Background()
	ds := New()

	key := datastore.NewKey("testkey")
	value := []byte("testvalue")

	err := ds.Put(ctx, key, value)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	err = ds.Delete(ctx, key)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	exists, err := ds.Has(ctx, key)
	if err != nil {
		t.Fatalf("Has failed: %v", err)
	}
	if exists {
		t.Fatalf("Should not have key %v after delete", key)
	}
}

func TestDatastore_Batch(t *testing.T) {
	ctx := context.Background()
	ds := New()

	batch, err := ds.Batch(ctx)
	if err != nil {
		t.Fatalf("Batch creation failed: %v", err)
	}

	key1 := datastore.NewKey("key1")
	value1 := []byte("value1")
	key2 := datastore.NewKey("key2")
	value2 := []byte("value2")

	err = batch.Put(ctx, key1, value1)
	if err != nil {
		t.Fatalf("Batch Put failed: %v", err)
	}
	err = batch.Put(ctx, key2, value2)
	if err != nil {
		t.Fatalf("Batch Put failed: %v", err)
	}

	err = batch.Commit(ctx)
	if err != nil {
		t.Fatalf("Batch Commit failed: %v", err)
	}

	exists, err := ds.Has(ctx, key1)
	if err != nil || !exists {
		t.Fatalf("Datastore should have key %v after batch commit", key1)
	}
	exists, err = ds.Has(ctx, key2)
	if err != nil || !exists {
		t.Fatalf("Datastore should have key %v after batch commit", key2)
	}
}

func TestDatastore_Query(t *testing.T) {
	ctx := context.Background()
	ds := New()

	for i := 0; i < 5; i++ {
		key := datastore.NewKey(fmt.Sprintf("key%d", i))
		value := []byte(fmt.Sprintf("value%d", i))
		err := ds.Put(ctx, key, value)
		if err != nil {
			t.Fatalf("Put failed: %v", err)
		}
	}

	q := query.Query{Prefix: "key"}

	results, err := ds.Query(ctx, q)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	entries, err := results.Rest()
	if err != nil {
		t.Fatalf("Failed to collect query results: %v", err)
	}

	if len(entries) != 5 {
		t.Fatalf("Expected 5 entries, got %d", len(entries))
	}
}

func TestDatastore_GetSize(t *testing.T) {
	ctx := context.Background()
	ds := New()

	key := datastore.NewKey("testkey")
	value := []byte("testvalue")

	err := ds.Put(ctx, key, value)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	size, err := ds.GetSize(ctx, key)
	if err != nil {
		t.Fatalf("GetSize failed: %v", err)
	}

	if size != len(value) {
		t.Fatalf("Expected size %d, got %d", len(value), size)
	}
}

func TestDatastore_Sync(t *testing.T) {
	ctx := context.Background()
	ds := New()

	err := ds.Sync(ctx, datastore.NewKey("testkey"))
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
}

func TestDatastore_Close(t *testing.T) {
	ds := New()

	err := ds.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestDatastore_QueryWithLimitOffset(t *testing.T) {
	ctx := context.Background()
	ds := New()

	for i := 0; i < 10; i++ {
		key := datastore.NewKey(fmt.Sprintf("key%d", i))
		value := []byte(fmt.Sprintf("value%d", i))
		err := ds.Put(ctx, key, value)
		if err != nil {
			t.Fatalf("Put failed: %v", err)
		}
	}

	tests := []struct {
		limit  int
		offset int
		expect int
	}{
		{limit: 5, offset: 0, expect: 5},
		{limit: 0, offset: 5, expect: 5},
		{limit: 3, offset: 3, expect: 3},
		{limit: 10, offset: 10, expect: 0},
		{limit: 20, offset: 0, expect: 10},
	}

	for _, tc := range tests {
		q := query.Query{Limit: tc.limit, Offset: tc.offset}
		results, err := ds.Query(ctx, q)
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}

		entries, err := results.Rest()
		if err != nil {
			t.Fatalf("Failed to collect query results: %v", err)
		}

		if len(entries) != tc.expect {
			t.Errorf("Expected %d entries, got %d for limit %d and offset %d", tc.expect, len(entries), tc.limit, tc.offset)
		}
	}
}

func TestDatastore_QueryKeysOnly(t *testing.T) {
	ctx := context.Background()
	ds := New()

	for i := 0; i < 5; i++ {
		key := datastore.NewKey(fmt.Sprintf("key%d", i))
		value := []byte(fmt.Sprintf("value%d", i))
		err := ds.Put(ctx, key, value)
		if err != nil {
			t.Fatalf("Put failed: %v", err)
		}
	}

	q := query.Query{KeysOnly: true}
	results, err := ds.Query(ctx, q)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	entries, err := results.Rest()
	if err != nil {
		t.Fatalf("Failed to collect query results: %v", err)
	}

	for _, e := range entries {
		if e.Value != nil {
			t.Errorf("Expected nil value for KeysOnly query, got %v", e.Value)
		}
	}
}

func TestDatastore_GetSizeNotFound(t *testing.T) {
	ctx := context.Background()
	ds := New()

	key := datastore.NewKey("nonexistentkey")

	size, err := ds.GetSize(ctx, key)
	if err != datastore.ErrNotFound {
		t.Fatalf("Expected ErrNotFound for non-existent key, got %v", err)
	}

	if size != -1 {
		t.Fatalf("Expected size -1 for non-existent key, got %d", size)
	}
}

func TestDatastore_CloseMultiple(t *testing.T) {
	ds := New()

	err := ds.Close()
	if err != nil {
		t.Fatalf("First Close failed: %v", err)
	}

	err = ds.Close()
	if err != nil {
		t.Fatalf("Subsequent Close failed: %v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	ds := New()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := datastore.NewKey(fmt.Sprintf("key%d", i))
			value := []byte(fmt.Sprintf("value%d", i))
			err := ds.Put(ctx, key, value)
			if err != nil {
				t.Errorf("Concurrent Put failed: %v", err)
			}
		}(i)
	}

	wg.Wait()

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := datastore.NewKey(fmt.Sprintf("key%d", i))
			value, err := ds.Get(ctx, key)
			if err != nil {
				t.Errorf("Concurrent Get failed: %v", err)
			}
			expectedValue := fmt.Sprintf("value%d", i)
			if string(value) != expectedValue {
				t.Errorf("Concurrent Get returned wrong value: got %v, want %v", string(value), expectedValue)
			}
		}(i)
	}

	wg.Wait()
}

func TestBatchMixedOperations(t *testing.T) {
	ctx := context.Background()
	ds := New()

	batch, err := ds.Batch(ctx)
	if err != nil {
		t.Fatalf("Batch creation failed: %v", err)
	}

	for i := 0; i < 10; i++ {
		key := datastore.NewKey(fmt.Sprintf("key%d", i))
		value := []byte(fmt.Sprintf("value%d", i))
		if i%2 == 0 {
			err = batch.Put(ctx, key, value)
		} else {
			err = batch.Delete(ctx, key)
		}
		if err != nil {
			t.Fatalf("Batch operation failed: %v", err)
		}
	}

	err = batch.Commit(ctx)
	if err != nil {
		t.Fatalf("Batch commit failed: %v", err)
	}

	for i := 0; i < 10; i++ {
		key := datastore.NewKey(fmt.Sprintf("key%d", i))
		_, err := ds.Get(ctx, key)
		if i%2 == 0 && err != nil {
			t.Errorf("Expected key%d to exist", i)
		} else if i%2 != 0 && err != datastore.ErrNotFound {
			t.Errorf("Expected key%d to not exist", i)
		}
	}
}

func TestQueryNoFiltersNoOrders(t *testing.T) {
	ctx := context.Background()
	ds := New()
	defer ds.Close()

	for i := 0; i < 5; i++ {
		key := datastore.NewKey(fmt.Sprintf("key%d", i))
		value := []byte(fmt.Sprintf("value%d", i))
		if err := ds.Put(ctx, key, value); err != nil {
			t.Fatalf("Put failed: %v", err)
		}
	}

	q := query.Query{}
	results, err := ds.Query(ctx, q)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	entries, err := results.Rest()
	if err != nil {
		t.Fatalf("Reading results failed: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("Expected 5 entries, got %d", len(entries))
	}
}

func TestClosedDatastore(t *testing.T) {
	ctx := context.Background()
	ds := New()
	if err := ds.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if _, err := ds.Get(ctx, datastore.NewKey("key")); !errors.Is(err, ErrClosed) {
		t.Errorf("Expected ErrClosed, got %v", err)
	}

	if err := ds.Put(ctx, datastore.NewKey("key"), []byte("value")); !errors.Is(err, ErrClosed) {
		t.Errorf("Expected ErrClosed, got %v", err)
	}

	if _, err := ds.Batch(ctx); !errors.Is(err, ErrClosed) {
		t.Errorf("Expected ErrClosed, got %v", err)
	}
}

func TestClosedDatastoreOperations(t *testing.T) {
	ctx := context.Background()
	ds := New()
	ds.Close()

	operations := []struct {
		name string
		fn   func() error
	}{
		{"Put", func() error { return ds.Put(ctx, datastore.NewKey("key"), []byte("value")) }},
		{"Get", func() error { _, err := ds.Get(ctx, datastore.NewKey("key")); return err }},
		{"GetSize", func() error { _, err := ds.GetSize(ctx, datastore.NewKey("key")); return err }},
		{"Has", func() error { _, err := ds.Has(ctx, datastore.NewKey("key")); return err }},
		{"Delete", func() error { return ds.Delete(ctx, datastore.NewKey("key")) }},
		{"Query", func() error { _, err := ds.Query(ctx, query.Query{}); return err }},
		{"Batch", func() error { _, err := ds.Batch(ctx); return err }},
	}

	for _, op := range operations {
		if err := op.fn(); err != ErrClosed {
			t.Errorf("%s did not return ErrClosed after datastore closure", op.name)
		}
	}
}

func TestBatchOperationsAfterDatastoreClosure(t *testing.T) {
	ctx := context.Background()
	ds := New()
	batch, _ := ds.Batch(ctx)
	ds.Close()

	if err := batch.Put(ctx, datastore.NewKey("key"), []byte("value")); err != ErrClosed {
		t.Errorf("Batch Put did not return ErrClosed after datastore closure")
	}

	if err := batch.Delete(ctx, datastore.NewKey("key")); err != ErrClosed {
		t.Errorf("Batch Delete did not return ErrClosed after datastore closure")
	}

	if err := batch.Commit(ctx); err != ErrClosed {
		t.Errorf("Batch Commit did not return ErrClosed after datastore closure")
	}
}

func TestGetSizeOnClosedDatastore(t *testing.T) {
	ctx := context.Background()
	ds := New()
	ds.Close()

	size, err := ds.GetSize(ctx, datastore.NewKey("key"))
	if err != ErrClosed {
		t.Errorf("GetSize did not return ErrClosed after datastore closure")
	}
	if size != -1 {
		t.Errorf("GetSize did not return -1 after datastore closure, got %d", size)
	}
}

func TestQueryWithFilters(t *testing.T) {
	ctx := context.Background()
	ds := New()
	defer ds.Close()

	testData := map[string]string{
		"/prefix/key1": "value1",
		"/prefix/key2": "value2",
		"/other/key3":  "value3",
		"/prefix/key4": "value4",
		"/other/key5":  "value5",
	}

	for k, v := range testData {
		err := ds.Put(ctx, datastore.NewKey(k), []byte(v))
		if err != nil {
			t.Fatalf("Put failed: %v", err)
		}
	}

	prefixFilter := query.FilterKeyPrefix{Prefix: "/prefix"}

	q := query.Query{
		Filters: []query.Filter{prefixFilter},
	}

	results, err := ds.Query(ctx, q)
	if err != nil {
		t.Fatalf("Query with filter failed: %v", err)
	}

	entries, err := results.Rest()
	if err != nil {
		t.Fatalf("Failed to collect query results: %v", err)
	}

	if len(entries) != 3 {
		t.Errorf("Expected 3 entries with prefix filter, got %d", len(entries))
	}

	for _, e := range entries {
		if !bytes.HasPrefix([]byte(e.Key), []byte("/prefix")) {
			t.Errorf("Entry %s should have /prefix prefix", e.Key)
		}
	}
}

func TestQueryWithOrders(t *testing.T) {
	ctx := context.Background()
	ds := New()
	defer ds.Close()

	keys := []string{"/c", "/a", "/b", "/e", "/d"}
	for _, k := range keys {
		err := ds.Put(ctx, datastore.NewKey(k), []byte("value-"+k))
		if err != nil {
			t.Fatalf("Put failed: %v", err)
		}
	}

	q := query.Query{
		Orders: []query.Order{query.OrderByKey{}},
	}

	results, err := ds.Query(ctx, q)
	if err != nil {
		t.Fatalf("Query with order failed: %v", err)
	}

	entries, err := results.Rest()
	if err != nil {
		t.Fatalf("Failed to collect query results: %v", err)
	}

	if len(entries) != 5 {
		t.Fatalf("Expected 5 entries, got %d", len(entries))
	}

	expectedOrder := []string{"/a", "/b", "/c", "/d", "/e"}
	for i, e := range entries {
		if e.Key != expectedOrder[i] {
			t.Errorf("Expected key at position %d to be %s, got %s", i, expectedOrder[i], e.Key)
		}
	}
}

func TestQueryWithMultipleFilters(t *testing.T) {
	ctx := context.Background()
	ds := New()
	defer ds.Close()

	// Populate datastore
	testData := map[string]string{
		"/app/user/1": "user1",
		"/app/user/2": "user2",
		"/app/post/1": "post1",
		"/app/post/2": "post2",
		"/sys/log/1":  "log1",
	}

	for k, v := range testData {
		err := ds.Put(ctx, datastore.NewKey(k), []byte(v))
		if err != nil {
			t.Fatalf("Put failed: %v", err)
		}
	}

	q := query.Query{
		Filters: []query.Filter{
			query.FilterKeyPrefix{Prefix: "/app"},
			query.FilterKeyPrefix{Prefix: "/app/user"},
		},
	}

	results, err := ds.Query(ctx, q)
	if err != nil {
		t.Fatalf("Query with multiple filters failed: %v", err)
	}

	entries, err := results.Rest()
	if err != nil {
		t.Fatalf("Failed to collect query results: %v", err)
	}

	if len(entries) != 2 {
		t.Errorf("Expected 2 entries with multiple filters, got %d", len(entries))
	}
}

func TestQueryWithFiltersAndOrders(t *testing.T) {
	ctx := context.Background()
	ds := New()
	defer ds.Close()

	// Populate datastore
	testData := map[string]string{
		"/data/z":  "z",
		"/data/a":  "a",
		"/data/m":  "m",
		"/other/x": "x",
	}

	for k, v := range testData {
		err := ds.Put(ctx, datastore.NewKey(k), []byte(v))
		if err != nil {
			t.Fatalf("Put failed: %v", err)
		}
	}

	q := query.Query{
		Filters: []query.Filter{query.FilterKeyPrefix{Prefix: "/data"}},
		Orders:  []query.Order{query.OrderByKey{}},
	}

	results, err := ds.Query(ctx, q)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	entries, err := results.Rest()
	if err != nil {
		t.Fatalf("Failed to collect query results: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("Expected 3 entries, got %d", len(entries))
	}

	expectedOrder := []string{"/data/a", "/data/m", "/data/z"}
	for i, e := range entries {
		if e.Key != expectedOrder[i] {
			t.Errorf("Expected key %s at position %d, got %s", expectedOrder[i], i, e.Key)
		}
	}
}

func TestQueryFilterExcludesAll(t *testing.T) {
	ctx := context.Background()
	ds := New()
	defer ds.Close()

	ds.Put(ctx, datastore.NewKey("/foo/bar"), []byte("baz"))
	ds.Put(ctx, datastore.NewKey("/foo/qux"), []byte("quux"))

	q := query.Query{
		Filters: []query.Filter{query.FilterKeyPrefix{Prefix: "/nonexistent"}},
	}

	results, err := ds.Query(ctx, q)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	entries, err := results.Rest()
	if err != nil {
		t.Fatalf("Failed to collect query results: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("Expected 0 entries when filter excludes all, got %d", len(entries))
	}
}
