package raft

import (
	"bytes"
	"io"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/require"
	"gotest.tools/v3/assert"
)

// TestKVSnapshot_Release_Comprehensive tests Release with various scenarios
func TestKVSnapshot_Release_Comprehensive(t *testing.T) {
	t.Run("with_data", func(t *testing.T) {
		snap := &kvSnapshot{
			data: map[string][]byte{
				"key1": []byte("value1"),
				"key2": []byte("value2"),
			},
		}

		// Release should be a no-op and not panic
		snap.Release()

		// Data should still be accessible (Release is a no-op)
		assert.Equal(t, len(snap.data), 2)
	})

	t.Run("empty", func(t *testing.T) {
		snap := &kvSnapshot{
			data: make(map[string][]byte),
		}

		snap.Release()
		assert.Equal(t, len(snap.data), 0)
	})

	t.Run("nil", func(t *testing.T) {
		snap := &kvSnapshot{
			data: nil,
		}

		snap.Release()
		assert.Assert(t, snap.data == nil)
	})

	t.Run("multiple_calls", func(t *testing.T) {
		snap := &kvSnapshot{
			data: map[string][]byte{"key": []byte("value")},
		}

		// Multiple calls should not panic
		snap.Release()
		snap.Release()
		snap.Release()

		assert.Equal(t, len(snap.data), 1)
	})
}

// TestKVSnapshot_Persist_Comprehensive tests Persist with various scenarios
func TestKVSnapshot_Persist_Comprehensive(t *testing.T) {
	t.Run("with_data", func(t *testing.T) {
		snap := &kvSnapshot{
			data: map[string][]byte{
				"key1": []byte("value1"),
				"key2": []byte("value2"),
			},
		}

		sink := &testSnapshotSinkComprehensive{
			buf: bytes.Buffer{},
		}

		err := snap.Persist(sink)
		require.NoError(t, err)

		// Verify data was written
		assert.Assert(t, sink.buf.Len() > 0)

		// Verify it's valid CBOR
		var restored map[string][]byte
		err = cbor.Unmarshal(sink.buf.Bytes(), &restored)
		require.NoError(t, err)
		assert.Equal(t, len(restored), 2)
		assert.Equal(t, string(restored["key1"]), "value1")
	})

	t.Run("empty", func(t *testing.T) {
		snap := &kvSnapshot{
			data: make(map[string][]byte),
		}

		sink := &testSnapshotSinkComprehensive{
			buf: bytes.Buffer{},
		}

		err := snap.Persist(sink)
		require.NoError(t, err)

		// Should write empty map
		var restored map[string][]byte
		err = cbor.Unmarshal(sink.buf.Bytes(), &restored)
		require.NoError(t, err)
		assert.Equal(t, len(restored), 0)
	})

	t.Run("large_data", func(t *testing.T) {
		// Create snapshot with large data
		data := make(map[string][]byte)
		for i := 0; i < 100; i++ {
			key := make([]byte, 100)
			value := make([]byte, 1000)
			data[string(key)] = value
		}

		snap := &kvSnapshot{data: data}

		sink := &testSnapshotSinkComprehensive{
			buf: bytes.Buffer{},
		}

		err := snap.Persist(sink)
		require.NoError(t, err)

		// Verify data was written
		assert.Assert(t, sink.buf.Len() > 0)
	})
}

// TestKVFSM_Restore_Comprehensive tests Restore with various scenarios
func TestKVFSM_Restore_Comprehensive(t *testing.T) {
	t.Run("from_empty", func(t *testing.T) {
		store := newTestStore()
		fsm := newKVFSM(store, "/raft/test")

		// Create snapshot data
		snapshotData := map[string][]byte{
			"key1": []byte("value1"),
			"key2": []byte("value2"),
		}

		data, err := cbor.Marshal(snapshotData)
		require.NoError(t, err)

		reader := io.NopCloser(bytes.NewReader(data))

		err = fsm.Restore(reader)
		require.NoError(t, err)

		// Verify data was restored
		val, found := fsm.Get("key1")
		assert.Assert(t, found)
		assert.Equal(t, string(val), "value1")

		val, found = fsm.Get("key2")
		assert.Assert(t, found)
		assert.Equal(t, string(val), "value2")
	})

	t.Run("overwrite_existing", func(t *testing.T) {
		store := newTestStore()
		fsm := newKVFSM(store, "/raft/test")

		// First add some data
		cmd := Command{
			Type: CommandSet,
			Set:  &SetCommand{Key: "oldkey", Value: []byte("oldvalue")},
		}
		cmdData, _ := cbor.Marshal(cmd)
		fsm.Apply(&raft.Log{Data: cmdData, Index: 1})

		// Verify it exists
		_, found := fsm.Get("oldkey")
		assert.Assert(t, found)

		// Restore with new data
		snapshotData := map[string][]byte{
			"newkey1": []byte("newvalue1"),
			"newkey2": []byte("newvalue2"),
		}

		data, err := cbor.Marshal(snapshotData)
		require.NoError(t, err)

		reader := io.NopCloser(bytes.NewReader(data))

		err = fsm.Restore(reader)
		require.NoError(t, err)

		// Old key should be gone
		_, found = fsm.Get("oldkey")
		assert.Assert(t, !found)

		// New keys should exist
		val, found := fsm.Get("newkey1")
		assert.Assert(t, found)
		assert.Equal(t, string(val), "newvalue1")
	})

	t.Run("empty_snapshot", func(t *testing.T) {
		store := newTestStore()
		fsm := newKVFSM(store, "/raft/test")

		// Add some data first
		cmd := Command{
			Type: CommandSet,
			Set:  &SetCommand{Key: "key", Value: []byte("value")},
		}
		cmdData, _ := cbor.Marshal(cmd)
		fsm.Apply(&raft.Log{Data: cmdData, Index: 1})

		// Restore with empty snapshot
		snapshotData := make(map[string][]byte)
		data, err := cbor.Marshal(snapshotData)
		require.NoError(t, err)

		reader := io.NopCloser(bytes.NewReader(data))

		err = fsm.Restore(reader)
		require.NoError(t, err)

		// Original key should be gone
		_, found := fsm.Get("key")
		assert.Assert(t, !found)
	})

	t.Run("invalid_cbor", func(t *testing.T) {
		store := newTestStore()
		fsm := newKVFSM(store, "/raft/test")

		reader := io.NopCloser(bytes.NewReader([]byte("invalid cbor data")))

		err := fsm.Restore(reader)
		require.Error(t, err)
	})

	t.Run("read_error", func(t *testing.T) {
		store := newTestStore()
		fsm := newKVFSM(store, "/raft/test")

		// Create a reader that will error on read
		reader := &errorReader{}

		err := fsm.Restore(reader)
		require.Error(t, err)
	})
}

// errorReader is a reader that always returns an error
type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, io.ErrUnexpectedEOF
}

func (e *errorReader) Close() error {
	return nil
}

// TestKVFSM_Restore_WithManyKeys tests Restore with many keys
func TestKVFSM_Restore_WithManyKeys(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	// Create snapshot with many keys
	snapshotData := make(map[string][]byte)
	for i := 0; i < 100; i++ {
		key := "key" + string(rune(i))
		value := []byte("value" + string(rune(i)))
		snapshotData[key] = value
	}

	data, err := cbor.Marshal(snapshotData)
	require.NoError(t, err)

	reader := io.NopCloser(bytes.NewReader(data))

	err = fsm.Restore(reader)
	require.NoError(t, err)

	// Verify all keys were restored
	for i := 0; i < 100; i++ {
		key := "key" + string(rune(i))
		val, found := fsm.Get(key)
		assert.Assert(t, found)
		expected := "value" + string(rune(i))
		assert.Equal(t, string(val), expected)
	}
}

// TestKVFSM_SnapshotAndRestore_RoundTrip_Comprehensive tests full snapshot/restore cycle
func TestKVFSM_SnapshotAndRestore_RoundTrip_Comprehensive(t *testing.T) {
	store1 := newTestStore()
	fsm1 := newKVFSM(store1, "/raft/test1")

	// Add data to first FSM
	keys := []string{"a", "b", "c", "prefix/x", "prefix/y"}
	for _, key := range keys {
		cmd := Command{
			Type: CommandSet,
			Set:  &SetCommand{Key: key, Value: []byte("value-" + key)},
		}
		cmdData, _ := cbor.Marshal(cmd)
		fsm1.Apply(&raft.Log{Data: cmdData, Index: uint64(len(keys))})
	}

	// Create snapshot
	snapshot, err := fsm1.Snapshot()
	require.NoError(t, err)

	// Persist snapshot
	sink := &testSnapshotSinkComprehensive{buf: bytes.Buffer{}}
	err = snapshot.Persist(sink)
	require.NoError(t, err)

	// Release snapshot
	snapshot.Release()

	// Create new FSM and restore
	store2 := newTestStore()
	fsm2 := newKVFSM(store2, "/raft/test2")

	reader := io.NopCloser(bytes.NewReader(sink.buf.Bytes()))
	err = fsm2.Restore(reader)
	require.NoError(t, err)

	// Verify all keys were restored
	for _, key := range keys {
		val, found := fsm2.Get(key)
		assert.Assert(t, found, "key %s should exist", key)
		assert.Equal(t, string(val), "value-"+key)
	}
}

// TestFsmAdapter_Restore_Comprehensive tests the fsmAdapter.Restore wrapper comprehensively
func TestFsmAdapter_Restore_Comprehensive(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	// Create snapshot data
	snapshotData := map[string][]byte{
		"key1": []byte("value1"),
		"key2": []byte("value2"),
	}

	data, err := cbor.Marshal(snapshotData)
	require.NoError(t, err)

	reader := io.NopCloser(bytes.NewReader(data))

	// Test Restore
	err = fsm.Restore(reader)
	require.NoError(t, err)

	// Verify data was restored
	val, found := fsm.Get("key1")
	assert.Assert(t, found)
	assert.Equal(t, string(val), "value1")
}

// testSnapshotSinkComprehensive is a test implementation of raft.SnapshotSink for comprehensive tests
type testSnapshotSinkComprehensive struct {
	buf bytes.Buffer
}

func (s *testSnapshotSinkComprehensive) ID() string {
	return "test-snapshot-id"
}

func (s *testSnapshotSinkComprehensive) Write(p []byte) (n int, err error) {
	return s.buf.Write(p)
}

func (s *testSnapshotSinkComprehensive) Close() error {
	return nil
}

func (s *testSnapshotSinkComprehensive) Cancel() error {
	return nil
}
