package kvdb

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	dssync "github.com/ipfs/go-datastore/sync"
	logging "github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/p2p/peer/peertest"
)

func TestKVDatabase_PutAndGet(t *testing.T) {
	mn := peertest.Net(t)
	logger := logging.Logger("test")
	mockNode := mockPeer(t, mn)
	f := opener{mockNode}
	db, _ := f.New(logger, "testpath", 10)

	key := "testkey"
	value := []byte("testvalue")
	err := db.Put(context.Background(), key, value)
	if err != nil {
		t.Fatalf("Failed to put value: %v", err)
	}

	retrievedValue, err := db.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Failed to get value: %v", err)
	}

	if string(retrievedValue) != string(value) {
		t.Fatalf("Expected retrieved value to be '%s', got '%s'", value, retrievedValue)
	}

	db.Close()
}

func TestKVDatabase_Delete(t *testing.T) {
	mn := peertest.Net(t)
	logger := logging.Logger("test")
	mockNode := mockPeer(t, mn)
	f := opener{mockNode}
	db, _ := f.New(logger, "testpath", 10)

	key := "testkey"
	value := []byte("testvalue")
	db.Put(context.Background(), key, value)
	err := db.Delete(context.Background(), key)
	if err != nil {
		t.Fatalf("Failed to delete value: %v", err)
	}

	_, err = db.Get(context.Background(), key)
	if err != ds.ErrNotFound {
		t.Fatalf("Expected ErrNotFound for deleted key, got %v", err)
	}

	db.Close()
}

func TestKVDatabase_List(t *testing.T) {
	mn := peertest.Net(t)
	logger := logging.Logger("test")
	mockNode := mockPeer(t, mn)
	f := opener{mockNode}
	db, _ := f.New(logger, "testpath", 10)

	keys := []string{"key1", "key2", "key3"}
	for _, key := range keys {
		err := db.Put(context.Background(), key, []byte("value"))
		if err != nil {
			t.Fatalf("Failed to put value for key '%s': %v", key, err)
		}
	}

	listedKeys, err := db.List(context.Background(), "")
	if err != nil {
		t.Fatalf("Failed to list keys: %v", err)
	}

	if len(listedKeys) != len(keys) {
		t.Fatalf("Expected %d keys, got %d", len(keys), len(listedKeys))
	}

	db.Close()
}

func TestKVDatabase_Batch(t *testing.T) {
	mn := peertest.Net(t)
	logger := logging.Logger("test")
	mockNode := mockPeer(t, mn)
	f := opener{mockNode}
	db, _ := f.New(logger, "testpath", 10)

	batch, err := db.Batch(context.Background())
	if err != nil {
		t.Fatalf("Failed to start batch: %v", err)
	}

	keys := []string{"batchkey1", "batchkey2"}
	for _, key := range keys {
		err := batch.Put(key, []byte("batchvalue"))
		if err != nil {
			t.Fatalf("Failed to put value in batch for key '%s': %v", key, err)
		}
	}

	err = batch.Commit()
	if err != nil {
		t.Fatalf("Failed to commit batch: %v", err)
	}

	for _, key := range keys {
		_, err := db.Get(context.Background(), key)
		if err != nil {
			t.Fatalf("Failed to get value for key '%s' after batch commit: %v", key, err)
		}
	}

	db.Close()
}

func TestKVDatabase_Get_NonExistentKey(t *testing.T) {
	mn := peertest.Net(t)
	logger := logging.Logger("test")
	mockNode := mockPeer(t, mn)
	f := opener{mockNode}
	db, _ := f.New(logger, "testpath", 10)

	_, err := db.Get(context.Background(), "nonexistentkey")
	if err != ds.ErrNotFound {
		t.Fatalf("Expected ErrNotFound for non-existent key, got %v", err)
	}

	db.Close()
}

func TestKVDatabase_Put_EmptyKeyOrValue(t *testing.T) {
	mn := peertest.Net(t)
	logger := logging.Logger("test")
	mockNode := mockPeer(t, mn)
	f := opener{mockNode}
	db, _ := f.New(logger, "testpath", 10)

	err := db.Put(context.Background(), "", []byte("value"))
	if err == nil {
		t.Fatalf("Expected error when putting empty key")
	}

	err = db.Put(context.Background(), "testkey", []byte(""))
	if err != nil {
		t.Fatalf("Failed to put empty value: %v", err)
	}

	db.Close()
}

func TestKVDatabase_List_SpecificPrefix(t *testing.T) {
	mn := peertest.Net(t)
	logger := logging.Logger("test")
	mockNode := mockPeer(t, mn)
	f := opener{mockNode}
	db, _ := f.New(logger, "testpath", 10)

	prefix := "myprefix/"
	keysWithPrefix := []string{prefix + "key1", prefix + "key2"}
	for _, key := range keysWithPrefix {
		err := db.Put(context.Background(), key, []byte("value"))
		if err != nil {
			t.Fatalf("Failed to put value for key '%s': %v", key, err)
		}
	}

	listedKeys, err := db.List(context.Background(), prefix)
	if err != nil {
		t.Fatalf("Failed to list keys with prefix '%s': %v", prefix, err)
	}

	if len(listedKeys) != len(keysWithPrefix) {
		t.Fatalf("Expected %d keys with prefix '%s', got %d", len(keysWithPrefix), prefix, len(listedKeys))
	}

	db.Close()
}

// scanFailing is a store whose scans, once failing is set, read one entry and then
// fail, as a disk that fails partway through a read does.
type scanFailing struct {
	ds.Batching
	failing atomic.Bool
}

var errScanFailed = errors.New("the disk failed partway through a scan")

func (s *scanFailing) Query(ctx context.Context, q query.Query) (query.Results, error) {
	results, err := s.Batching.Query(ctx, q)
	if err != nil || !s.failing.Load() {
		return results, err
	}
	read, failed := 0, false
	return query.ResultsFromIterator(q, query.Iterator{
		Next: func() (query.Result, bool) {
			if failed {
				return query.Result{}, false
			}
			if read++; read > 1 {
				failed = true
				return query.Result{Error: errScanFailed}, true
			}
			return results.NextSync()
		},
		Close: results.Close,
	}), nil
}

// A LIST THE STORE FAILS PARTWAY IS AN ERROR, never the keys it read before
// failing: a partial list read as complete undercounts every quota, claim and sweep
// built on it.
func TestAListTheStoreFailsPartwayIsAnError(t *testing.T) {
	store := &scanFailing{Batching: dssync.MutexWrap(ds.NewMapDatastore())}
	db := &kvDatabase{datastore: manualReplica(t, store, newFetchThrough(), "list fails: ")}
	ctx := t.Context()
	for _, key := range []string{"/a", "/b", "/c"} {
		if err := db.Put(ctx, key, []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := db.List(ctx, "")
	if err != nil || len(keys) != 3 {
		t.Fatalf("listed %v, %v; want the three keys", keys, err)
	}

	store.failing.Store(true)
	if keys, err := db.List(ctx, ""); !errors.Is(err, errScanFailed) {
		t.Fatalf("a scan that failed partway listed %v, %v", keys, err)
	}
	if keys, err := db.ListRegEx(ctx, "", ".*"); !errors.Is(err, errScanFailed) {
		t.Fatalf("a regex scan that failed partway listed %v, %v", keys, err)
	}
}

// A LIST WHOSE CONTEXT ENDED IS AN ERROR: the store ends a query its context ended
// as it ends a finished one.
func TestAListCutShortByItsContextIsAnError(t *testing.T) {
	db := &kvDatabase{datastore: manualReplica(t, dssync.MutexWrap(ds.NewMapDatastore()), newFetchThrough(), "list cut short: ")}
	if err := db.Put(t.Context(), "/a", []byte("v")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if keys, err := db.List(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("a list whose context ended listed %v, %v", keys, err)
	}
}
