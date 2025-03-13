package kvdb

import (
	"context"
	"sync"
	"testing"

	ds "github.com/ipfs/go-datastore"
	logging "github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/p2p/peer"
)

func TestFactory_New(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.Mock(context.Background())
	f := New(mockNode)

	// Test creating a new database
	db, err := f.New(logger, "testpath", 10)
	if err != nil {
		t.Fatalf("Failed to create new database: %v", err)
	}

	// Assert that db is not nil
	if db == nil {
		t.Fatal("Expected non-nil db, got nil")
	}

	// Cleanup
	db.Close()
}

func TestFactory_getDB(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.Mock(context.Background())
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	// Test retrieving an existing database
	retrievedDB := f.(*factory).getDB("testpath")

	// Assert that retrievedDB is not nil and is equal to the original db
	if retrievedDB == nil {
		t.Fatal("Expected non-nil retrievedDB, got nil")
	}
	if retrievedDB != db {
		t.Fatal("Expected retrievedDB to be equal to original db")
	}

	// Cleanup
	db.Close()
}

func TestFactory_deleteDB(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.Mock(context.Background())
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	// Test deleting a database
	f.(*factory).deleteDB("testpath")

	// Assert that the database is no longer retrievable
	deletedDB := f.(*factory).getDB("testpath")
	if deletedDB != nil {
		t.Fatal("Expected deletedDB to be nil")
	}

	// Cleanup
	db.Close()
}

func TestKVDatabase_Close(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.Mock(context.Background())
	factory := New(mockNode)
	db, _ := factory.New(logger, "testpath", 10)

	// Test closing the database
	db.Close()

	// Assert that the database is closed
	// You would need to add a method or a way to check if the database is closed.
	// For example, you could add a `IsClosed() bool` method to the kvDatabase type.
	if !db.(*kvDatabase).closed {
		t.Fatal("Expected database to be closed")
	}
}

func TestFactory_NewDatabaseExists(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.Mock(context.Background())
	f := New(mockNode)

	// Test creating a new database
	db1, err := f.New(logger, "testpath", 10)
	if err != nil {
		t.Fatalf("Failed to create new database: %v", err)
	}

	// Test creating the same database again should retrieve the existing one
	db2, err := f.New(logger, "testpath", 10)
	if err != nil {
		t.Fatalf("Failed to create new database: %v", err)
	}

	// Assert that db2 is not nil and is equal to db1
	if db2 == nil {
		t.Fatal("Expected non-nil db2, got nil")
	}
	if db1 != db2 {
		t.Fatal("Expected db2 to be equal to db1")
	}

	// Cleanup
	db1.Close()
}

func TestFactory_ConcurrentAccess(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.Mock(context.Background())
	f := New(mockNode)

	// Test concurrent creation of databases
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

	// Assert that both databases are created and retrievable
	if db1 := f.(*factory).getDB("testpath1"); db1 == nil {
		t.Fatal("Expected non-nil db1, got nil")
	}
	if db2 := f.(*factory).getDB("testpath2"); db2 == nil {
		t.Fatal("Expected non-nil db2, got nil")
	}

	// Cleanup
	f.(*factory).getDB("testpath1").Close()
	f.(*factory).getDB("testpath2").Close()
}

func TestFactory_CloseAll(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.Mock(context.Background())
	f := New(mockNode)

	// Create multiple databases
	f.New(logger, "testpath1", 10)
	f.New(logger, "testpath2", 10)

	// Test closing all databases
	f.Close()

	// Assert that all databases are closed
	if db1 := f.(*factory).getDB("testpath1"); db1 != nil && !db1.closed {
		t.Fatal("Expected db1 to be closed")
	}
	if db2 := f.(*factory).getDB("testpath2"); db2 != nil && !db2.closed {
		t.Fatal("Expected db2 to be closed")
	}
}

func TestKVDatabase_ReopenClosedDatabase(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.Mock(context.Background())
	f := New(mockNode)

	// Create a database and close it
	db, _ := f.New(logger, "testpath", 10)
	db.Close()

	// Test reopening the closed database
	reopenedDB, err := f.New(logger, "testpath", 10)
	if err != nil {
		t.Fatalf("Failed to reopen closed database: %v", err)
	}

	// Assert that reopenedDB is not nil and is a new instance
	if reopenedDB == nil {
		t.Fatal("Expected non-nil reopenedDB, got nil")
	}
	if reopenedDB == db {
		t.Fatal("Expected reopenedDB to be a new instance")
	}

	// Cleanup
	reopenedDB.Close()
}

func TestKVDatabase_ListAsync(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.Mock(context.Background())
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	// Put some values
	keys := []string{"async1", "async2", "async3"}
	for _, key := range keys {
		err := db.Put(context.Background(), key, []byte("value"))
		if err != nil {
			t.Fatalf("Failed to put value for key '%s': %v", key, err)
		}
	}

	// Test ListAsync
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure the context is canceled to prevent leaks

	keyChan, err := db.ListAsync(ctx, "")
	if err != nil {
		t.Fatalf("Failed to list keys asynchronously: %v", err)
	}

	receivedKeys := make([]string, 0, len(keys))
	for key := range keyChan {
		receivedKeys = append(receivedKeys, key)
	}

	// Assert that all keys are received
	if len(receivedKeys) != len(keys) {
		t.Fatalf("Expected %d keys, got %d", len(keys), len(receivedKeys))
	}

	// Cleanup
	db.Close()
}

func TestKVDatabase_Batch_MixedOperations(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.Mock(context.Background())
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	// Create a batch
	batch, err := db.Batch(context.Background())
	if err != nil {
		t.Fatalf("Failed to create batch: %v", err)
	}

	// Perform some operations
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

	// Commit the batch
	err = batch.Commit()
	if err != nil {
		t.Fatalf("Failed to commit batch: %v", err)
	}

	// Assert that "batchkey1" was deleted and "batchkey2" exists
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

	// Cleanup
	db.Close()
}

func TestKVDatabase_Sync(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.Mock(context.Background())
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	// Put a value
	key := "syncKey"
	err := db.Put(context.Background(), key, []byte("syncValue"))
	if err != nil {
		t.Fatalf("Failed to put value: %v", err)
	}

	// Sync the key
	err = db.Sync(context.Background(), key)
	if err != nil {
		t.Fatalf("Failed to sync: %v", err)
	}

	// Assert that the key is still there after sync
	value, err := db.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Failed to get key after sync: %v", err)
	}
	if string(value) != "syncValue" {
		t.Fatalf("Expected value 'syncValue', got '%s'", value)
	}

	// Cleanup
	db.Close()
}
