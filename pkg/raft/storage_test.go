package raft

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/raft"
	"github.com/ipfs/go-datastore"
	dsync "github.com/ipfs/go-datastore/sync"
)

func newTestDatastore() datastore.Batching {
	return dsync.MutexWrap(datastore.NewMapDatastore())
}

func TestLogStore_StoreAndRetrieve(t *testing.T) {
	store := newTestDatastore()
	logStore := newLogStore(store, "/raft/test/log")

	log := &raft.Log{
		Index: 1,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  []byte("test data"),
	}

	if err := logStore.StoreLog(log); err != nil {
		t.Fatalf("failed to store log: %v", err)
	}

	var retrieved raft.Log
	if err := logStore.GetLog(1, &retrieved); err != nil {
		t.Fatalf("failed to get log: %v", err)
	}

	if retrieved.Index != log.Index {
		t.Errorf("expected index %d, got %d", log.Index, retrieved.Index)
	}
	if retrieved.Term != log.Term {
		t.Errorf("expected term %d, got %d", log.Term, retrieved.Term)
	}
	if string(retrieved.Data) != string(log.Data) {
		t.Errorf("expected data '%s', got '%s'", log.Data, retrieved.Data)
	}
}

func TestLogStore_StoreLogs(t *testing.T) {
	store := newTestDatastore()
	logStore := newLogStore(store, "/raft/test/log")

	logs := []*raft.Log{
		{Index: 1, Term: 1, Type: raft.LogCommand, Data: []byte("data1")},
		{Index: 2, Term: 1, Type: raft.LogCommand, Data: []byte("data2")},
		{Index: 3, Term: 2, Type: raft.LogCommand, Data: []byte("data3")},
	}

	if err := logStore.StoreLogs(logs); err != nil {
		t.Fatalf("failed to store logs: %v", err)
	}

	for _, log := range logs {
		var retrieved raft.Log
		if err := logStore.GetLog(log.Index, &retrieved); err != nil {
			t.Errorf("failed to get log %d: %v", log.Index, err)
			continue
		}
		if string(retrieved.Data) != string(log.Data) {
			t.Errorf("log %d: expected data '%s', got '%s'", log.Index, log.Data, retrieved.Data)
		}
	}
}

func TestLogStore_FirstLastIndex(t *testing.T) {
	store := newTestDatastore()
	logStore := newLogStore(store, "/raft/test/log")

	// Empty initially
	first, err := logStore.FirstIndex()
	if err != nil {
		t.Fatalf("failed to get first index: %v", err)
	}
	if first != 0 {
		t.Errorf("expected first index 0 for empty store, got %d", first)
	}

	last, err := logStore.LastIndex()
	if err != nil {
		t.Fatalf("failed to get last index: %v", err)
	}
	if last != 0 {
		t.Errorf("expected last index 0 for empty store, got %d", last)
	}

	// Add logs
	logs := []*raft.Log{
		{Index: 5, Term: 1, Data: []byte("a")},
		{Index: 6, Term: 1, Data: []byte("b")},
		{Index: 7, Term: 1, Data: []byte("c")},
		{Index: 10, Term: 2, Data: []byte("d")},
	}
	if err := logStore.StoreLogs(logs); err != nil {
		t.Fatalf("failed to store logs: %v", err)
	}

	first, _ = logStore.FirstIndex()
	last, _ = logStore.LastIndex()

	if first != 5 {
		t.Errorf("expected first index 5, got %d", first)
	}
	if last != 10 {
		t.Errorf("expected last index 10, got %d", last)
	}
}

func TestLogStore_GetLog_NotFound(t *testing.T) {
	store := newTestDatastore()
	logStore := newLogStore(store, "/raft/test/log")

	var log raft.Log
	err := logStore.GetLog(999, &log)
	if err != raft.ErrLogNotFound {
		t.Errorf("expected ErrLogNotFound, got %v", err)
	}
}

func TestLogStore_DeleteRange(t *testing.T) {
	store := newTestDatastore()
	logStore := newLogStore(store, "/raft/test/log")

	// Add logs
	logs := []*raft.Log{
		{Index: 1, Term: 1, Data: []byte("a")},
		{Index: 2, Term: 1, Data: []byte("b")},
		{Index: 3, Term: 1, Data: []byte("c")},
		{Index: 4, Term: 1, Data: []byte("d")},
		{Index: 5, Term: 1, Data: []byte("e")},
	}
	if err := logStore.StoreLogs(logs); err != nil {
		t.Fatalf("failed to store logs: %v", err)
	}

	// Delete range 2-4
	if err := logStore.DeleteRange(2, 4); err != nil {
		t.Fatalf("failed to delete range: %v", err)
	}

	// Log 1 should exist
	var log raft.Log
	if err := logStore.GetLog(1, &log); err != nil {
		t.Errorf("expected log 1 to exist: %v", err)
	}

	// Log 5 should exist
	if err := logStore.GetLog(5, &log); err != nil {
		t.Errorf("expected log 5 to exist: %v", err)
	}

	// Logs 2-4 should not exist
	for i := uint64(2); i <= 4; i++ {
		err := logStore.GetLog(i, &log)
		if err != raft.ErrLogNotFound {
			t.Errorf("expected log %d to be deleted", i)
		}
	}
}

func TestStableStore_SetGet(t *testing.T) {
	store := newTestDatastore()
	stableStore := newStableStore(store, "/raft/test/stable")

	key := []byte("test-key")
	value := []byte("test-value")

	if err := stableStore.Set(key, value); err != nil {
		t.Fatalf("failed to set: %v", err)
	}

	got, err := stableStore.Get(key)
	if err != nil {
		t.Fatalf("failed to get: %v", err)
	}

	if string(got) != string(value) {
		t.Errorf("expected '%s', got '%s'", value, got)
	}
}

func TestStableStore_Get_NotFound(t *testing.T) {
	store := newTestDatastore()
	stableStore := newStableStore(store, "/raft/test/stable")

	got, err := stableStore.Get([]byte("nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for nonexistent key, got '%s'", got)
	}
}

func TestStableStore_SetGetUint64(t *testing.T) {
	store := newTestDatastore()
	stableStore := newStableStore(store, "/raft/test/stable")

	key := []byte("term")
	value := uint64(12345)

	if err := stableStore.SetUint64(key, value); err != nil {
		t.Fatalf("failed to set uint64: %v", err)
	}

	got, err := stableStore.GetUint64(key)
	if err != nil {
		t.Fatalf("failed to get uint64: %v", err)
	}

	if got != value {
		t.Errorf("expected %d, got %d", value, got)
	}
}

func TestStableStore_GetUint64_NotFound(t *testing.T) {
	store := newTestDatastore()
	stableStore := newStableStore(store, "/raft/test/stable")

	got, err := stableStore.GetUint64([]byte("nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 0 {
		t.Errorf("expected 0 for nonexistent key, got %d", got)
	}
}

func TestStableStore_GetUint64_InvalidLength(t *testing.T) {
	store := newTestDatastore()
	stableStore := newStableStore(store, "/raft/test/stable")

	// Store invalid length value
	key := []byte("bad")
	if err := stableStore.Set(key, []byte("short")); err != nil {
		t.Fatalf("failed to set: %v", err)
	}

	_, err := stableStore.GetUint64(key)
	if err == nil {
		t.Error("expected error for invalid length")
	}
}

func TestSnapshotStore_CreateListOpen(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-snapshot-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	snapStore, err := newSnapshotStore(dir, 3)
	if err != nil {
		t.Fatalf("failed to create snapshot store: %v", err)
	}

	// List should be empty initially
	snapshots, err := snapStore.List()
	if err != nil {
		t.Fatalf("failed to list snapshots: %v", err)
	}
	if len(snapshots) != 0 {
		t.Errorf("expected 0 snapshots, got %d", len(snapshots))
	}

	// Create a snapshot
	config := raft.Configuration{
		Servers: []raft.Server{
			{ID: "node1", Address: "addr1"},
		},
	}

	sink, err := snapStore.Create(raft.SnapshotVersionMax, 10, 2, config, 5, nil)
	if err != nil {
		t.Fatalf("failed to create snapshot: %v", err)
	}

	// Write some data
	testData := []byte("snapshot data")
	if _, err := sink.Write(testData); err != nil {
		t.Fatalf("failed to write to snapshot: %v", err)
	}

	if err := sink.Close(); err != nil {
		t.Fatalf("failed to close snapshot: %v", err)
	}

	// List should have one snapshot
	snapshots, err = snapStore.List()
	if err != nil {
		t.Fatalf("failed to list snapshots: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snapshots))
	}

	// Verify metadata
	meta := snapshots[0]
	if meta.Index != 10 {
		t.Errorf("expected index 10, got %d", meta.Index)
	}
	if meta.Term != 2 {
		t.Errorf("expected term 2, got %d", meta.Term)
	}

	// Open and read
	openMeta, reader, err := snapStore.Open(meta.ID)
	if err != nil {
		t.Fatalf("failed to open snapshot: %v", err)
	}
	defer reader.Close()

	if openMeta.Index != meta.Index {
		t.Errorf("expected index %d, got %d", meta.Index, openMeta.Index)
	}

	// Read data
	data := make([]byte, len(testData))
	n, err := reader.Read(data)
	if err != nil {
		t.Fatalf("failed to read snapshot: %v", err)
	}
	if string(data[:n]) != string(testData) {
		t.Errorf("expected data '%s', got '%s'", testData, data[:n])
	}
}

func TestSnapshotStore_Reap(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-snapshot-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	// Retain only 2 snapshots
	snapStore, err := newSnapshotStore(dir, 2)
	if err != nil {
		t.Fatalf("failed to create snapshot store: %v", err)
	}

	config := raft.Configuration{
		Servers: []raft.Server{{ID: "node1", Address: "addr1"}},
	}

	// Create 4 snapshots
	for i := uint64(1); i <= 4; i++ {
		sink, err := snapStore.Create(raft.SnapshotVersionMax, i*10, i, config, i, nil)
		if err != nil {
			t.Fatalf("failed to create snapshot %d: %v", i, err)
		}
		sink.Write([]byte("data"))
		sink.Close()
	}

	// Should have only 2 snapshots (retention)
	snapshots, err := snapStore.List()
	if err != nil {
		t.Fatalf("failed to list snapshots: %v", err)
	}
	if len(snapshots) != 2 {
		t.Errorf("expected 2 snapshots after reaping, got %d", len(snapshots))
	}

	// The highest index snapshots should be retained
	if snapshots[0].Index != 40 {
		t.Errorf("expected highest index 40, got %d", snapshots[0].Index)
	}
	if snapshots[1].Index != 30 {
		t.Errorf("expected second highest index 30, got %d", snapshots[1].Index)
	}
}

func TestSnapshotSink_Cancel(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-snapshot-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	snapStore, err := newSnapshotStore(dir, 3)
	if err != nil {
		t.Fatalf("failed to create snapshot store: %v", err)
	}

	config := raft.Configuration{}
	sink, err := snapStore.Create(raft.SnapshotVersionMax, 10, 2, config, 5, nil)
	if err != nil {
		t.Fatalf("failed to create snapshot: %v", err)
	}

	sink.Write([]byte("data"))
	sink.Cancel()

	// Should have no snapshots after cancel
	snapshots, err := snapStore.List()
	if err != nil {
		t.Fatalf("failed to list snapshots: %v", err)
	}
	if len(snapshots) != 0 {
		t.Errorf("expected 0 snapshots after cancel, got %d", len(snapshots))
	}
}

func TestLogStoreCreation(t *testing.T) {
	store := newTestDatastore()
	logStore := newLogStore(store, "/raft/test/log")

	if logStore == nil {
		t.Fatal("expected non-nil log store")
	}
	if logStore.prefix != "/raft/test/log" {
		t.Errorf("expected prefix '/raft/test/log', got '%s'", logStore.prefix)
	}
}

func TestStableStoreCreation(t *testing.T) {
	store := newTestDatastore()
	stableStore := newStableStore(store, "/raft/test/stable")

	if stableStore == nil {
		t.Fatal("expected non-nil stable store")
	}
	if stableStore.prefix != "/raft/test/stable" {
		t.Errorf("expected prefix '/raft/test/stable', got '%s'", stableStore.prefix)
	}
}

func TestSnapshotSink_ID(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-snapshot-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	snapStore, err := newSnapshotStore(dir, 3)
	if err != nil {
		t.Fatalf("failed to create snapshot store: %v", err)
	}

	config := raft.Configuration{}
	sink, err := snapStore.Create(raft.SnapshotVersionMax, 10, 2, config, 5, nil)
	if err != nil {
		t.Fatalf("failed to create snapshot: %v", err)
	}
	defer sink.Cancel()

	id := sink.ID()
	if id == "" {
		t.Error("expected non-empty snapshot ID")
	}
}

func TestSnapshotStore_List_EmptyDir(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-snapshot-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	snapStore, err := newSnapshotStore(dir, 3)
	if err != nil {
		t.Fatalf("failed to create snapshot store: %v", err)
	}

	// Create a non-snapshot directory
	nonSnapDir := dir + "/not-a-snapshot"
	if err := os.MkdirAll(nonSnapDir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	// List should ignore directories without meta.cbor
	snapshots, err := snapStore.List()
	if err != nil {
		t.Fatalf("failed to list: %v", err)
	}
	if len(snapshots) != 0 {
		t.Errorf("expected 0 snapshots, got %d", len(snapshots))
	}
}

func TestSnapshotSink_Close_Twice(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-snapshot-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	snapStore, err := newSnapshotStore(dir, 3)
	if err != nil {
		t.Fatalf("failed to create snapshot store: %v", err)
	}

	config := raft.Configuration{}
	sink, err := snapStore.Create(raft.SnapshotVersionMax, 10, 2, config, 5, nil)
	if err != nil {
		t.Fatalf("failed to create snapshot: %v", err)
	}

	sink.Write([]byte("data"))
	sink.Close()

	// Second close should be no-op
	if err := sink.Close(); err != nil {
		t.Errorf("second close should be no-op, got: %v", err)
	}
}

func TestSnapshotSink_Cancel_Twice(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-snapshot-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	snapStore, err := newSnapshotStore(dir, 3)
	if err != nil {
		t.Fatalf("failed to create snapshot store: %v", err)
	}

	config := raft.Configuration{}
	sink, err := snapStore.Create(raft.SnapshotVersionMax, 10, 2, config, 5, nil)
	if err != nil {
		t.Fatalf("failed to create snapshot: %v", err)
	}

	sink.Cancel()

	// Second cancel should be no-op
	if err := sink.Cancel(); err != nil {
		t.Errorf("second cancel should be no-op, got: %v", err)
	}
}

func TestSnapshotSink_Write_ThenClose(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-snapshot-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	snapStore, err := newSnapshotStore(dir, 3)
	if err != nil {
		t.Fatalf("failed to create snapshot store: %v", err)
	}

	config := raft.Configuration{}
	sink, err := snapStore.Create(raft.SnapshotVersionMax, 10, 2, config, 5, nil)
	if err != nil {
		t.Fatalf("failed to create snapshot: %v", err)
	}

	// Write some data
	n, err := sink.Write([]byte("test data"))
	if err != nil {
		t.Fatalf("failed to write: %v", err)
	}
	if n != 9 {
		t.Errorf("expected 9 bytes written, got %d", n)
	}

	// Close the sink
	if err := sink.Close(); err != nil {
		t.Fatalf("failed to close sink: %v", err)
	}

	// Verify ID is accessible
	id := sink.ID()
	if id == "" {
		t.Error("expected non-empty ID")
	}
}

func TestLogStore_FirstIndex_Empty(t *testing.T) {
	store := dsync.MutexWrap(datastore.NewMapDatastore())
	logStore := newLogStore(store, "/raft/test/log")

	// Empty store should return 0
	idx, err := logStore.FirstIndex()
	if err != nil {
		t.Fatalf("failed to get first index: %v", err)
	}
	if idx != 0 {
		t.Errorf("expected 0 for empty store, got %d", idx)
	}
}

func TestLogStore_LastIndex_Empty(t *testing.T) {
	store := dsync.MutexWrap(datastore.NewMapDatastore())
	logStore := newLogStore(store, "/raft/test/log")

	// Empty store should return 0
	idx, err := logStore.LastIndex()
	if err != nil {
		t.Fatalf("failed to get last index: %v", err)
	}
	if idx != 0 {
		t.Errorf("expected 0 for empty store, got %d", idx)
	}
}

func TestStableStore_Get_NilValue(t *testing.T) {
	store := dsync.MutexWrap(datastore.NewMapDatastore())
	stableStore := newStableStore(store, "/raft/test/stable")

	// Getting non-existent key should return nil, nil
	val, err := stableStore.Get([]byte("nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != nil {
		t.Errorf("expected nil value, got %v", val)
	}
}

func TestLogStore_DeleteRange_Empty(t *testing.T) {
	store := dsync.MutexWrap(datastore.NewMapDatastore())
	logStore := newLogStore(store, "/raft/test/log")

	// Deleting from empty store should not error
	err := logStore.DeleteRange(0, 100)
	if err != nil {
		t.Fatalf("failed to delete range: %v", err)
	}
}

func TestSnapshotStore_Open_NotFound(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-snapshot-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	snapStore, err := newSnapshotStore(dir, 3)
	if err != nil {
		t.Fatalf("failed to create snapshot store: %v", err)
	}

	// Opening non-existent snapshot should fail
	_, _, err = snapStore.Open("nonexistent-snapshot")
	if err == nil {
		t.Error("expected error opening non-existent snapshot")
	}
}

func TestSnapshotStore_List_CorruptedMeta(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-snapshot-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	snapStore, err := newSnapshotStore(dir, 3)
	if err != nil {
		t.Fatalf("failed to create snapshot store: %v", err)
	}

	// Create a corrupted snapshot directory
	corruptedDir := filepath.Join(dir, "corrupted-snapshot")
	if err := os.MkdirAll(corruptedDir, 0755); err != nil {
		t.Fatalf("failed to create corrupted dir: %v", err)
	}

	// Create invalid meta file
	if err := os.WriteFile(filepath.Join(corruptedDir, "meta.cbor"), []byte("invalid cbor"), 0644); err != nil {
		t.Fatalf("failed to create corrupted meta: %v", err)
	}

	// List should skip corrupted snapshot
	snapshots, err := snapStore.List()
	if err != nil {
		t.Fatalf("failed to list snapshots: %v", err)
	}
	if len(snapshots) != 0 {
		t.Errorf("expected 0 valid snapshots, got %d", len(snapshots))
	}
}

func TestLogStore_StoreLogs_Multiple(t *testing.T) {
	store := dsync.MutexWrap(datastore.NewMapDatastore())
	logStore := newLogStore(store, "/raft/test/log")

	// Store multiple logs at once
	logs := []*raft.Log{
		{Index: 1, Term: 1, Type: raft.LogCommand, Data: []byte("cmd1")},
		{Index: 2, Term: 1, Type: raft.LogCommand, Data: []byte("cmd2")},
		{Index: 3, Term: 1, Type: raft.LogCommand, Data: []byte("cmd3")},
	}

	if err := logStore.StoreLogs(logs); err != nil {
		t.Fatalf("failed to store logs: %v", err)
	}

	// Verify all logs
	for _, log := range logs {
		var retrieved raft.Log
		if err := logStore.GetLog(log.Index, &retrieved); err != nil {
			t.Errorf("failed to get log %d: %v", log.Index, err)
		}
	}
}

func TestLogStore_DeleteRange_Partial(t *testing.T) {
	store := dsync.MutexWrap(datastore.NewMapDatastore())
	logStore := newLogStore(store, "/raft/test/log")

	// Store 5 logs
	for i := uint64(1); i <= 5; i++ {
		log := &raft.Log{Index: i, Term: 1, Type: raft.LogCommand, Data: []byte("data")}
		if err := logStore.StoreLog(log); err != nil {
			t.Fatalf("failed to store log %d: %v", i, err)
		}
	}

	// Delete range 2-4
	if err := logStore.DeleteRange(2, 4); err != nil {
		t.Fatalf("failed to delete range: %v", err)
	}

	// Verify first and last still exist
	var log1, log5 raft.Log
	if err := logStore.GetLog(1, &log1); err != nil {
		t.Error("log 1 should still exist")
	}
	if err := logStore.GetLog(5, &log5); err != nil {
		t.Error("log 5 should still exist")
	}

	// Verify middle logs deleted
	var log3 raft.Log
	if err := logStore.GetLog(3, &log3); err == nil {
		t.Error("log 3 should be deleted")
	}
}

func TestLogStore_FirstLastIndex_AfterStoring(t *testing.T) {
	store := dsync.MutexWrap(datastore.NewMapDatastore())
	logStore := newLogStore(store, "/raft/test/log")

	// Store logs 5, 10, 15
	for _, idx := range []uint64{5, 10, 15} {
		log := &raft.Log{Index: idx, Term: 1, Type: raft.LogCommand, Data: []byte("data")}
		if err := logStore.StoreLog(log); err != nil {
			t.Fatalf("failed to store log %d: %v", idx, err)
		}
	}

	first, err := logStore.FirstIndex()
	if err != nil {
		t.Fatalf("failed to get first: %v", err)
	}
	if first != 5 {
		t.Errorf("expected first index 5, got %d", first)
	}

	last, err := logStore.LastIndex()
	if err != nil {
		t.Fatalf("failed to get last: %v", err)
	}
	if last != 15 {
		t.Errorf("expected last index 15, got %d", last)
	}
}

func TestNewSnapshotStore_InvalidDir(t *testing.T) {
	// Try to create snapshot store in an invalid path
	// Use /dev/null which is not a directory
	_, err := newSnapshotStore("/dev/null/invalid-path", 3)
	if err == nil {
		t.Error("expected error for invalid directory")
	}
}

func TestSnapshotStore_Create_AndVerify(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-snapshot-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	snapStore, err := newSnapshotStore(dir, 3)
	if err != nil {
		t.Fatalf("failed to create snapshot store: %v", err)
	}

	config := raft.Configuration{
		Servers: []raft.Server{
			{ID: "server1", Address: "addr1"},
		},
	}

	sink, err := snapStore.Create(raft.SnapshotVersionMax, 100, 5, config, 90, nil)
	if err != nil {
		t.Fatalf("failed to create snapshot: %v", err)
	}

	// Write data
	_, err = sink.Write([]byte("snapshot data"))
	if err != nil {
		t.Fatalf("failed to write to sink: %v", err)
	}

	// Close the sink
	if err := sink.Close(); err != nil {
		t.Fatalf("failed to close sink: %v", err)
	}

	// List snapshots
	snaps, err := snapStore.List()
	if err != nil {
		t.Fatalf("failed to list: %v", err)
	}
	if len(snaps) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(snaps))
	}

	// Open and read
	meta, rc, err := snapStore.Open(sink.ID())
	if err != nil {
		t.Fatalf("failed to open: %v", err)
	}
	defer rc.Close()

	if meta.Index != 100 {
		t.Errorf("expected index 100, got %d", meta.Index)
	}
	if meta.Term != 5 {
		t.Errorf("expected term 5, got %d", meta.Term)
	}
}
