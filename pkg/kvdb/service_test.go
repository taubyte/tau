package kvdb

import (
	"context"
	"sync"
	"testing"

	ds "github.com/ipfs/go-datastore"
	logging "github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/p2p/peer"
)

// Its repository is a temporary directory only Close removes.
func mockPeer(t testing.TB) peer.Node {
	n := peer.Mock(t.Context())
	t.Cleanup(n.Close)
	return n
}

func TestFactory_New(t *testing.T) {
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	f := New(mockNode)

	db, err := f.New(logger, "testpath", 10)
	if err != nil {
		t.Fatalf("Failed to create new database: %v", err)
	}

	if db == nil {
		t.Fatal("Expected non-nil db, got nil")
	}

	db.Close()
}

func TestFactory_getDB(t *testing.T) {
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	retrievedDB := f.(*factory).getDB("testpath")

	if retrievedDB == nil {
		t.Fatal("Expected non-nil retrievedDB, got nil")
	}
	if retrievedDB != db {
		t.Fatal("Expected retrievedDB to be equal to original db")
	}

	db.Close()
}

func TestFactory_deleteDB(t *testing.T) {
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	f.(*factory).deleteDB("testpath")

	deletedDB := f.(*factory).getDB("testpath")
	if deletedDB != nil {
		t.Fatal("Expected deletedDB to be nil")
	}

	db.Close()
}

func TestKVDatabase_Close(t *testing.T) {
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	factory := New(mockNode)
	db, _ := factory.New(logger, "testpath", 10)

	db.Close()

	if !db.(*kvDatabase).closed {
		t.Fatal("Expected database to be closed")
	}
}

func TestFactory_NewDatabaseExists(t *testing.T) {
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	f := New(mockNode)

	db1, err := f.New(logger, "testpath", 10)
	if err != nil {
		t.Fatalf("Failed to create new database: %v", err)
	}

	db2, err := f.New(logger, "testpath", 10)
	if err != nil {
		t.Fatalf("Failed to create new database: %v", err)
	}

	if db2 == nil {
		t.Fatal("Expected non-nil db2, got nil")
	}
	if db1 != db2 {
		t.Fatal("Expected db2 to be equal to db1")
	}

	db1.Close()
}

func TestFactory_ConcurrentAccess(t *testing.T) {
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	f := New(mockNode)

	var wg sync.WaitGroup
	createDB := func(path string) {
		defer wg.Done()
		if _, err := f.New(logger, path, 10); err != nil {
			t.Errorf("Failed to create new database: %v", err)
		}
	}

	wg.Add(2)
	go createDB("testpath1")
	go createDB("testpath2")
	wg.Wait()

	if db1 := f.(*factory).getDB("testpath1"); db1 == nil {
		t.Fatal("Expected non-nil db1, got nil")
	}
	if db2 := f.(*factory).getDB("testpath2"); db2 == nil {
		t.Fatal("Expected non-nil db2, got nil")
	}

	f.(*factory).getDB("testpath1").Close()
	f.(*factory).getDB("testpath2").Close()
}

func TestFactory_CloseAll(t *testing.T) {
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	f := New(mockNode)

	f.New(logger, "testpath1", 10)
	f.New(logger, "testpath2", 10)

	f.Close()

	if db1 := f.(*factory).getDB("testpath1"); db1 != nil && !db1.closed {
		t.Fatal("Expected db1 to be closed")
	}
	if db2 := f.(*factory).getDB("testpath2"); db2 != nil && !db2.closed {
		t.Fatal("Expected db2 to be closed")
	}
}

func TestKVDatabase_ReopenClosedDatabase(t *testing.T) {
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	f := New(mockNode)

	db, _ := f.New(logger, "testpath", 10)
	db.Close()

	reopenedDB, err := f.New(logger, "testpath", 10)
	if err != nil {
		t.Fatalf("Failed to reopen closed database: %v", err)
	}

	if reopenedDB == nil {
		t.Fatal("Expected non-nil reopenedDB, got nil")
	}
	if reopenedDB == db {
		t.Fatal("Expected reopenedDB to be a new instance")
	}

	reopenedDB.Close()
}

func TestKVDatabase_ListAsync(t *testing.T) {
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	keys := []string{"async1", "async2", "async3"}
	for _, key := range keys {
		err := db.Put(context.Background(), key, []byte("value"))
		if err != nil {
			t.Fatalf("Failed to put value for key '%s': %v", key, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	keyChan, err := db.ListAsync(ctx, "")
	if err != nil {
		t.Fatalf("Failed to list keys asynchronously: %v", err)
	}

	receivedKeys := make([]string, 0, len(keys))
	for key := range keyChan {
		receivedKeys = append(receivedKeys, key)
	}

	if len(receivedKeys) != len(keys) {
		t.Fatalf("Expected %d keys, got %d", len(keys), len(receivedKeys))
	}

	db.Close()
}

func TestKVDatabase_Batch_MixedOperations(t *testing.T) {
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	batch, err := db.Batch(context.Background())
	if err != nil {
		t.Fatalf("Failed to create batch: %v", err)
	}

	err = batch.Put("batchkey1", []byte("value1"))
	if err != nil {
		t.Fatalf("Failed to put in batch: %v", err)
	}
	err = batch.Delete("batchkey1")
	if err != nil {
		t.Fatalf("Failed to delete in batch: %v", err)
	}
	err = batch.Put("batchkey2", []byte("value2"))
	if err != nil {
		t.Fatalf("Failed to put in batch: %v", err)
	}

	err = batch.Commit()
	if err != nil {
		t.Fatalf("Failed to commit batch: %v", err)
	}

	_, err = db.Get(context.Background(), "batchkey1")
	if err != ds.ErrNotFound {
		t.Fatalf("Expected 'batchkey1' to be deleted")
	}

	value, err := db.Get(context.Background(), "batchkey2")
	if err != nil {
		t.Fatalf("Failed to get 'batchkey2': %v", err)
	}
	if string(value) != "value2" {
		t.Fatalf("Expected 'batchkey2' to have value 'value2', got '%s'", value)
	}

	db.Close()
}

func TestKVDatabase_Sync(t *testing.T) {
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	key := "syncKey"
	err := db.Put(context.Background(), key, []byte("syncValue"))
	if err != nil {
		t.Fatalf("Failed to put value: %v", err)
	}

	err = db.Sync(context.Background(), key)
	if err != nil {
		t.Fatalf("Failed to sync: %v", err)
	}

	value, err := db.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Failed to get key after sync: %v", err)
	}
	if string(value) != "syncValue" {
		t.Fatalf("Expected value 'syncValue', got '%s'", value)
	}

	db.Close()
}
