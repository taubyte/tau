package kvdb

import (
	"context"
	"testing"

	ds "github.com/ipfs/go-datastore"
	logging "github.com/ipfs/go-log/v2"
)

func TestKVDatabase_PutAndGet(t *testing.T) {
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	f := New(mockNode)
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
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	f := New(mockNode)
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
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	f := New(mockNode)
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
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	f := New(mockNode)
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
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	f := New(mockNode)
	db, _ := f.New(logger, "testpath", 10)

	_, err := db.Get(context.Background(), "nonexistentkey")
	if err != ds.ErrNotFound {
		t.Fatalf("Expected ErrNotFound for non-existent key, got %v", err)
	}

	db.Close()
}

func TestKVDatabase_Put_EmptyKeyOrValue(t *testing.T) {
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	f := New(mockNode)
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
	logger := logging.Logger("test")
	mockNode := mockPeer(t)
	f := New(mockNode)
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
