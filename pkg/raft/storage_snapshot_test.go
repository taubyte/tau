package raft

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/require"
	"gotest.tools/v3/assert"
)

// TestDeleteRange_Comprehensive tests DeleteRange with various scenarios
func TestDeleteRange_Comprehensive(t *testing.T) {
	store := newTestDatastore()
	logStore := newLogStore(store, "/raft/test/log")

	// Store logs
	logs := []*raft.Log{
		{Index: 1, Term: 1, Type: raft.LogCommand, Data: []byte("log1")},
		{Index: 2, Term: 1, Type: raft.LogCommand, Data: []byte("log2")},
		{Index: 3, Term: 1, Type: raft.LogCommand, Data: []byte("log3")},
		{Index: 4, Term: 2, Type: raft.LogCommand, Data: []byte("log4")},
		{Index: 5, Term: 2, Type: raft.LogCommand, Data: []byte("log5")},
	}

	require.NoError(t, logStore.StoreLogs(logs))

	// Test deleting single log
	err := logStore.DeleteRange(3, 3)
	require.NoError(t, err)

	// Verify log 3 is deleted
	var log raft.Log
	err = logStore.GetLog(3, &log)
	assert.ErrorIs(t, err, raft.ErrLogNotFound)

	// Verify other logs still exist
	require.NoError(t, logStore.GetLog(1, &log))
	require.NoError(t, logStore.GetLog(2, &log))
	require.NoError(t, logStore.GetLog(4, &log))
	require.NoError(t, logStore.GetLog(5, &log))

	// Test deleting range that includes all remaining
	err = logStore.DeleteRange(1, 5)
	require.NoError(t, err)

	// All should be deleted
	for i := uint64(1); i <= 5; i++ {
		err := logStore.GetLog(i, &log)
		assert.ErrorIs(t, err, raft.ErrLogNotFound)
	}
}

// TestDeleteRange_EmptyRange tests DeleteRange with empty store
func TestDeleteRange_EmptyRange(t *testing.T) {
	store := newTestDatastore()
	logStore := newLogStore(store, "/raft/test/log")

	// Delete from empty store should not error
	err := logStore.DeleteRange(1, 10)
	require.NoError(t, err)
}

// TestDeleteRange_NonExistentRange tests DeleteRange with non-existent indices
func TestDeleteRange_NonExistentRange(t *testing.T) {
	store := newTestDatastore()
	logStore := newLogStore(store, "/raft/test/log")

	// Store one log
	log := &raft.Log{Index: 5, Term: 1, Type: raft.LogCommand, Data: []byte("log5")}
	require.NoError(t, logStore.StoreLog(log))

	// Delete range that doesn't include the stored log
	err := logStore.DeleteRange(1, 4)
	require.NoError(t, err)

	// Log 5 should still exist
	var retrieved raft.Log
	require.NoError(t, logStore.GetLog(5, &retrieved))
}

// TestSnapshotStore_Create_Comprehensive tests Create with various scenarios
func TestSnapshotStore_Create_Comprehensive(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := newSnapshotStore(tmpDir, 3)
	require.NoError(t, err)

	// Create snapshot
	config := raft.Configuration{
		Servers: []raft.Server{
			{ID: "1", Address: "addr1", Suffrage: raft.Voter},
		},
	}

	sink, err := store.Create(raft.SnapshotVersion(1), 10, 2, config, 5, nil)
	require.NoError(t, err)
	assert.Assert(t, sink != nil)

	// Verify ID is set
	id := sink.ID()
	assert.Assert(t, id != "")

	// Write some data
	data := []byte("snapshot data")
	n, err := sink.Write(data)
	require.NoError(t, err)
	assert.Equal(t, n, len(data))

	// Close to finalize
	require.NoError(t, sink.Close())

	// Verify snapshot was created
	snapshots, err := store.List()
	require.NoError(t, err)
	assert.Equal(t, len(snapshots), 1)
	assert.Equal(t, snapshots[0].Index, uint64(10))
	assert.Equal(t, snapshots[0].Term, uint64(2))
}

// TestSnapshotStore_Open_Comprehensive tests Open with various scenarios
func TestSnapshotStore_Open_Comprehensive(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := newSnapshotStore(tmpDir, 3)
	require.NoError(t, err)

	// Create and close a snapshot first
	config := raft.Configuration{
		Servers: []raft.Server{
			{ID: "1", Address: "addr1", Suffrage: raft.Voter},
		},
	}

	sink, err := store.Create(raft.SnapshotVersion(1), 10, 2, config, 5, nil)
	require.NoError(t, err)

	data := []byte("test snapshot data")
	_, err = sink.Write(data)
	require.NoError(t, err)

	id := sink.ID()
	require.NoError(t, sink.Close())

	// Now open it
	meta, reader, err := store.Open(id)
	require.NoError(t, err)
	defer reader.Close()

	assert.Equal(t, meta.Index, uint64(10))
	assert.Equal(t, meta.Term, uint64(2))
	assert.Equal(t, meta.ID, id)

	// Read the data
	readData, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, string(readData), string(data))
}

// TestSnapshotStore_Open_NotFound_Comprehensive tests Open with non-existent snapshot
func TestSnapshotStore_Open_NotFound_Comprehensive(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := newSnapshotStore(tmpDir, 3)
	require.NoError(t, err)

	_, _, err = store.Open("non-existent-id")
	require.Error(t, err)
}

// TestSnapshotSink_Cancel_Comprehensive tests Cancel functionality
func TestSnapshotSink_Cancel_Comprehensive(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := newSnapshotStore(tmpDir, 3)
	require.NoError(t, err)

	config := raft.Configuration{
		Servers: []raft.Server{
			{ID: "1", Address: "addr1", Suffrage: raft.Voter},
		},
	}

	sink, err := store.Create(raft.SnapshotVersion(1), 10, 2, config, 5, nil)
	require.NoError(t, err)

	// Write some data
	_, err = sink.Write([]byte("data"))
	require.NoError(t, err)

	// Cancel should remove the directory
	err = sink.Cancel()
	require.NoError(t, err)

	// Verify directory was removed
	id := sink.ID()
	dir := filepath.Join(tmpDir, id)
	_, err = os.Stat(dir)
	require.Error(t, err)
	assert.Assert(t, os.IsNotExist(err))

	// Cancel again should be no-op
	err = sink.Cancel()
	require.NoError(t, err)
}

// TestSnapshotSink_Write_AfterClose tests writing after close
func TestSnapshotSink_Write_AfterClose(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := newSnapshotStore(tmpDir, 3)
	require.NoError(t, err)

	config := raft.Configuration{
		Servers: []raft.Server{
			{ID: "1", Address: "addr1", Suffrage: raft.Voter},
		},
	}

	sink, err := store.Create(raft.SnapshotVersion(1), 10, 2, config, 5, nil)
	require.NoError(t, err)

	require.NoError(t, sink.Close())

	// Writing after close should still work (buffer is still writable)
	// but the data won't be persisted
	n, err := sink.Write([]byte("after close"))
	// This might succeed or fail depending on implementation
	_ = n
	_ = err
}

// TestSnapshotStore_Reap_Comprehensive tests the reap function
func TestSnapshotStore_Reap_Comprehensive(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := newSnapshotStore(tmpDir, 2) // Retain only 2
	require.NoError(t, err)

	config := raft.Configuration{
		Servers: []raft.Server{
			{ID: "1", Address: "addr1", Suffrage: raft.Voter},
		},
	}

	// Create 3 snapshots
	for i := 0; i < 3; i++ {
		sink, err := store.Create(raft.SnapshotVersion(1), uint64(10+i), 2, config, 5, nil)
		require.NoError(t, err)

		_, err = sink.Write([]byte("data"))
		require.NoError(t, err)

		require.NoError(t, sink.Close())
	}

	// Should only have 2 snapshots (oldest should be reaped)
	snapshots, err := store.List()
	require.NoError(t, err)
	assert.Equal(t, len(snapshots), 2)

	// The two newest should remain
	assert.Equal(t, snapshots[0].Index, uint64(12)) // Newest first
	assert.Equal(t, snapshots[1].Index, uint64(11))
}

// TestSnapshotStore_Reap_ExactRetain tests reap when exactly at retain limit
func TestSnapshotStore_Reap_ExactRetain(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := newSnapshotStore(tmpDir, 2) // Retain 2
	require.NoError(t, err)

	config := raft.Configuration{
		Servers: []raft.Server{
			{ID: "1", Address: "addr1", Suffrage: raft.Voter},
		},
	}

	// Create exactly 2 snapshots
	for i := 0; i < 2; i++ {
		sink, err := store.Create(raft.SnapshotVersion(1), uint64(10+i), 2, config, 5, nil)
		require.NoError(t, err)

		_, err = sink.Write([]byte("data"))
		require.NoError(t, err)

		require.NoError(t, sink.Close())
	}

	// Should have exactly 2 snapshots
	snapshots, err := store.List()
	require.NoError(t, err)
	assert.Equal(t, len(snapshots), 2)
}

// TestSnapshotStore_Reap_None tests reap when below retain limit
func TestSnapshotStore_Reap_None(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := newSnapshotStore(tmpDir, 5) // Retain 5
	require.NoError(t, err)

	config := raft.Configuration{
		Servers: []raft.Server{
			{ID: "1", Address: "addr1", Suffrage: raft.Voter},
		},
	}

	// Create only 2 snapshots
	for i := 0; i < 2; i++ {
		sink, err := store.Create(raft.SnapshotVersion(1), uint64(10+i), 2, config, 5, nil)
		require.NoError(t, err)

		_, err = sink.Write([]byte("data"))
		require.NoError(t, err)

		require.NoError(t, sink.Close())
	}

	// Should have both snapshots (none reaped)
	snapshots, err := store.List()
	require.NoError(t, err)
	assert.Equal(t, len(snapshots), 2)
}
