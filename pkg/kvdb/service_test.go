package kvdb

import (
	"context"
	"testing"

	ds "github.com/ipfs/go-datastore"
	logging "github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/peer/peertest"
)

// Its repository is a temporary directory only Close removes.
func mockPeer(t testing.TB, mn *peer.Mocknet) peer.Node {
	n := mn.Mock(t.Context())
	t.Cleanup(n.Close)
	return n
}

func TestOpen(t *testing.T) {
	mn := peertest.Net(t)
	logger := logging.Logger("test")
	mockNode := mockPeer(t, mn)
	f := opener{mockNode}

	db, err := f.New(logger, "testpath", 10)
	if err != nil {
		t.Fatalf("Failed to create new database: %v", err)
	}

	if db == nil {
		t.Fatal("Expected non-nil db, got nil")
	}

	db.Close()
}

func TestKVDatabase_Close(t *testing.T) {
	mn := peertest.Net(t)
	logger := logging.Logger("test")
	mockNode := mockPeer(t, mn)
	factory := opener{mockNode}
	db, _ := factory.New(logger, "testpath", 10)

	db.Close()

	if !db.closed {
		t.Fatal("Expected database to be closed")
	}
}

func TestKVDatabase_ReopenClosedDatabase(t *testing.T) {
	mn := peertest.Net(t)
	logger := logging.Logger("test")
	mockNode := mockPeer(t, mn)
	f := opener{mockNode}

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

func TestKVDatabase_Batch_MixedOperations(t *testing.T) {
	mn := peertest.Net(t)
	logger := logging.Logger("test")
	mockNode := mockPeer(t, mn)
	f := opener{mockNode}
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
	mn := peertest.Net(t)
	logger := logging.Logger("test")
	mockNode := mockPeer(t, mn)
	f := opener{mockNode}
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
