package kvdb

import (
	"context"
	"testing"

	ds "github.com/ipfs/go-datastore"
	logging "github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/p2p/peer"
)

func TestKVDatabase_PutAndGet(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.MockNode(context.Background())
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	// Test putting a value
	key := "testkey"
	value := []byte("testvalue")
	err := db.Put(context.Background(), key, value)
	if err != nil {
		t.Fatalf("Failed to put value: %v", err)
	}

	// Test getting the value back
	retrievedValue, err := db.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Failed to get value: %v", err)
	}

	// Assert that the retrieved value is equal to the original value
	if string(retrievedValue) != string(value) {
		t.Fatalf("Expected retrieved value to be '%s', got '%s'", value, retrievedValue)
	}

	// Cleanup
	db.Close()
}

func TestKVDatabase_Delete(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.MockNode(context.Background())
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	// Put a value and then delete it
	key := "testkey"
	value := []byte("testvalue")
	db.Put(context.Background(), key, value)
	err := db.Delete(context.Background(), key)
	if err != nil {
		t.Fatalf("Failed to delete value: %v", err)
	}

	// Try to get the deleted value
	_, err = db.Get(context.Background(), key)
	if err != ds.ErrNotFound {
		t.Fatalf("Expected ErrNotFound for deleted key, got %v", err)
	}

	// Cleanup
	db.Close()
}

func TestKVDatabase_List(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.MockNode(context.Background())
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	// Put some values
	keys := []string{"key1", "key2", "key3"}
	for _, key := range keys {
		err := db.Put(context.Background(), key, []byte("value"))
		if err != nil {
			t.Fatalf("Failed to put value for key '%s': %v", key, err)
		}
	}

	// List keys
	listedKeys, err := db.List(context.Background(), "")
	if err != nil {
		t.Fatalf("Failed to list keys: %v", err)
	}

	// Assert that all keys are listed
	if len(listedKeys) != len(keys) {
		t.Fatalf("Expected %d keys, got %d", len(keys), len(listedKeys))
	}

	// Cleanup
	db.Close()
}

func TestKVDatabase_Batch(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.MockNode(context.Background())
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	// Start a batch
	batch, err := db.Batch(context.Background())
	if err != nil {
		t.Fatalf("Failed to start batch: %v", err)
	}

	// Put some values in the batch
	keys := []string{"batchkey1", "batchkey2"}
	for _, key := range keys {
		err := batch.Put(key, []byte("batchvalue"))
		if err != nil {
			t.Fatalf("Failed to put value in batch for key '%s': %v", key, err)
		}
	}

	// Commit the batch
	err = batch.Commit()
	if err != nil {
		t.Fatalf("Failed to commit batch: %v", err)
	}

	// Assert that the values are stored
	for _, key := range keys {
		_, err := db.Get(context.Background(), key)
		if err != nil {
			t.Fatalf("Failed to get value for key '%s' after batch commit: %v", key, err)
		}
	}

	// Cleanup
	db.Close()
}

func TestKVDatabase_Get_NonExistentKey(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.MockNode(context.Background())
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	// Test getting a non-existent key
	_, err := db.Get(context.Background(), "nonexistentkey")
	if err != ds.ErrNotFound {
		t.Fatalf("Expected ErrNotFound for non-existent key, got %v", err)
	}

	// Cleanup
	db.Close()
}

func TestKVDatabase_Put_EmptyKeyOrValue(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.MockNode(context.Background())
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	// Test putting an empty key
	err := db.Put(context.Background(), "", []byte("value"))
	if err == nil {
		t.Fatalf("Expected error when putting empty key")
	}

	// Test putting an empty value
	err = db.Put(context.Background(), "testkey", []byte(""))
	if err != nil {
		t.Fatalf("Failed to put empty value: %v", err)
	}

	// Cleanup
	db.Close()
}

func TestKVDatabase_List_SpecificPrefix(t *testing.T) {
	// Setup
	logger := logging.Logger("test")
	mockNode := peer.MockNode(context.Background())
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	// Put some values with specific prefix
	prefix := "myprefix/"
	keysWithPrefix := []string{prefix + "key1", prefix + "key2"}
	for _, key := range keysWithPrefix {
		err := db.Put(context.Background(), key, []byte("value"))
		if err != nil {
			t.Fatalf("Failed to put value for key '%s': %v", key, err)
		}
	}

	// List keys with specific prefix
	listedKeys, err := db.List(context.Background(), prefix)
	if err != nil {
		t.Fatalf("Failed to list keys with prefix '%s': %v", prefix, err)
	}

	// Assert that only keys with the specific prefix are listed
	if len(listedKeys) != len(keysWithPrefix) {
		t.Fatalf("Expected %d keys with prefix '%s', got %d", len(keysWithPrefix), prefix, len(listedKeys))
	}

	// Cleanup
	db.Close()
}
