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

func TestKVSnapshot_Release_Comprehensive(t *testing.T) {
	t.Run("with_data", func(t *testing.T) {
		snap := &kvSnapshot{
			data: map[string]CRDTEntry{
				"key1": {Value: []byte("value1"), Timestamp: 1, WallClock: 100},
				"key2": {Value: []byte("value2"), Timestamp: 2, WallClock: 200},
			},
		}
		snap.Release()
		assert.Equal(t, len(snap.data), 2)
	})

	t.Run("empty", func(t *testing.T) {
		snap := &kvSnapshot{
			data: make(map[string]CRDTEntry),
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
			data: map[string]CRDTEntry{"key": {Value: []byte("value"), Timestamp: 1}},
		}
		snap.Release()
		snap.Release()
		snap.Release()
		assert.Equal(t, len(snap.data), 1)
	})
}

func TestKVSnapshot_Persist_Comprehensive(t *testing.T) {
	t.Run("with_data", func(t *testing.T) {
		snap := &kvSnapshot{
			data: map[string]CRDTEntry{
				"key1": {Value: []byte("value1"), Timestamp: 1, WallClock: 100},
				"key2": {Value: []byte("value2"), Timestamp: 2, WallClock: 200},
			},
			clock: 2,
		}

		sink := &testSnapshotSinkComprehensive{buf: bytes.Buffer{}}
		err := snap.Persist(sink)
		require.NoError(t, err)
		assert.Assert(t, sink.buf.Len() > 0)

		var restored snapshotPayload
		err = cbor.Unmarshal(sink.buf.Bytes(), &restored)
		require.NoError(t, err)
		assert.Equal(t, len(restored.Data), 2)
		assert.Equal(t, string(restored.Data["key1"].Value), "value1")
		assert.Equal(t, restored.Clock, uint64(2))
	})

	t.Run("empty", func(t *testing.T) {
		snap := &kvSnapshot{
			data: make(map[string]CRDTEntry),
		}
		sink := &testSnapshotSinkComprehensive{buf: bytes.Buffer{}}
		err := snap.Persist(sink)
		require.NoError(t, err)

		var restored snapshotPayload
		err = cbor.Unmarshal(sink.buf.Bytes(), &restored)
		require.NoError(t, err)
		assert.Equal(t, len(restored.Data), 0)
	})

	t.Run("large_data", func(t *testing.T) {
		data := make(map[string]CRDTEntry)
		for i := 0; i < 100; i++ {
			key := make([]byte, 100)
			value := make([]byte, 1000)
			data[string(key)] = CRDTEntry{Value: value, Timestamp: uint64(i + 1)}
		}
		snap := &kvSnapshot{data: data}
		sink := &testSnapshotSinkComprehensive{buf: bytes.Buffer{}}
		err := snap.Persist(sink)
		require.NoError(t, err)
		assert.Assert(t, sink.buf.Len() > 0)
	})
}

func TestKVFSM_Restore_Comprehensive(t *testing.T) {
	t.Run("from_new_format", func(t *testing.T) {
		store := newTestStore()
		fsm := newKVFSM(t.Context(), store, "/raft/test")

		payload := snapshotPayload{
			Data: map[string]CRDTEntry{
				"key1": {Value: []byte("value1"), Timestamp: 1, WallClock: 100},
				"key2": {Value: []byte("value2"), Timestamp: 2, WallClock: 200},
			},
			Clock: 2,
		}
		data, err := cbor.Marshal(payload)
		require.NoError(t, err)

		err = fsm.Restore(io.NopCloser(bytes.NewReader(data)))
		require.NoError(t, err)

		val, found := fsm.Get("key1")
		assert.Assert(t, found)
		assert.Equal(t, string(val), "value1")

		val, found = fsm.Get("key2")
		assert.Assert(t, found)
		assert.Equal(t, string(val), "value2")
	})

	t.Run("overwrite_existing", func(t *testing.T) {
		store := newTestStore()
		fsm := newKVFSM(t.Context(), store, "/raft/test")

		cmd := Command{
			Type: CommandSet,
			Set:  &SetCommand{Key: "oldkey", Value: []byte("oldvalue")},
		}
		cmdData, _ := cbor.Marshal(cmd)
		fsm.Apply(&raft.Log{Data: cmdData, Index: 1})

		_, found := fsm.Get("oldkey")
		assert.Assert(t, found)

		payload := snapshotPayload{
			Data: map[string]CRDTEntry{
				"newkey1": {Value: []byte("newvalue1"), Timestamp: 1},
				"newkey2": {Value: []byte("newvalue2"), Timestamp: 2},
			},
			Clock: 2,
		}
		data, err := cbor.Marshal(payload)
		require.NoError(t, err)

		err = fsm.Restore(io.NopCloser(bytes.NewReader(data)))
		require.NoError(t, err)

		_, found = fsm.Get("oldkey")
		assert.Assert(t, !found)

		val, found := fsm.Get("newkey1")
		assert.Assert(t, found)
		assert.Equal(t, string(val), "newvalue1")
	})

	t.Run("empty_snapshot", func(t *testing.T) {
		store := newTestStore()
		fsm := newKVFSM(t.Context(), store, "/raft/test")

		cmd := Command{
			Type: CommandSet,
			Set:  &SetCommand{Key: "key", Value: []byte("value")},
		}
		cmdData, _ := cbor.Marshal(cmd)
		fsm.Apply(&raft.Log{Data: cmdData, Index: 1})

		payload := snapshotPayload{Data: map[string]CRDTEntry{}}
		data, err := cbor.Marshal(payload)
		require.NoError(t, err)

		err = fsm.Restore(io.NopCloser(bytes.NewReader(data)))
		require.NoError(t, err)

		_, found := fsm.Get("key")
		assert.Assert(t, !found)
	})

	t.Run("invalid_cbor", func(t *testing.T) {
		store := newTestStore()
		fsm := newKVFSM(t.Context(), store, "/raft/test")

		err := fsm.Restore(io.NopCloser(bytes.NewReader([]byte("invalid cbor data"))))
		require.Error(t, err)
	})

	t.Run("read_error", func(t *testing.T) {
		store := newTestStore()
		fsm := newKVFSM(t.Context(), store, "/raft/test")

		err := fsm.Restore(&errorReader{})
		require.Error(t, err)
	})
}

type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, io.ErrUnexpectedEOF
}

func (e *errorReader) Close() error {
	return nil
}

func TestKVFSM_Restore_WithManyKeys(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, "/raft/test")

	entries := make(map[string]CRDTEntry)
	for i := 0; i < 100; i++ {
		key := "key" + string(rune(i))
		entries[key] = CRDTEntry{
			Value:     []byte("value" + string(rune(i))),
			Timestamp: uint64(i + 1),
		}
	}

	payload := snapshotPayload{Data: entries, Clock: 100}
	data, err := cbor.Marshal(payload)
	require.NoError(t, err)

	err = fsm.Restore(io.NopCloser(bytes.NewReader(data)))
	require.NoError(t, err)

	for i := 0; i < 100; i++ {
		key := "key" + string(rune(i))
		val, found := fsm.Get(key)
		assert.Assert(t, found)
		expected := "value" + string(rune(i))
		assert.Equal(t, string(val), expected)
	}
}

func TestKVFSM_SnapshotAndRestore_RoundTrip_Comprehensive(t *testing.T) {
	store1 := newTestStore()
	fsm1 := newKVFSM(t.Context(), store1, "/raft/test1")

	keys := []string{"a", "b", "c", "prefix/x", "prefix/y"}
	for _, key := range keys {
		cmd := Command{
			Type: CommandSet,
			Set:  &SetCommand{Key: key, Value: []byte("value-" + key)},
		}
		cmdData, _ := cbor.Marshal(cmd)
		fsm1.Apply(&raft.Log{Data: cmdData, Index: uint64(len(keys))})
	}

	snapshot, err := fsm1.Snapshot()
	require.NoError(t, err)

	sink := &testSnapshotSinkComprehensive{buf: bytes.Buffer{}}
	err = snapshot.Persist(sink)
	require.NoError(t, err)
	snapshot.Release()

	store2 := newTestStore()
	fsm2 := newKVFSM(t.Context(), store2, "/raft/test2")

	err = fsm2.Restore(io.NopCloser(bytes.NewReader(sink.buf.Bytes())))
	require.NoError(t, err)

	for _, key := range keys {
		val, found := fsm2.Get(key)
		assert.Assert(t, found, "key %s should exist", key)
		assert.Equal(t, string(val), "value-"+key)
	}
}

func TestFsmAdapter_Restore_Comprehensive(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, "/raft/test")

	payload := snapshotPayload{
		Data: map[string]CRDTEntry{
			"key1": {Value: []byte("value1"), Timestamp: 1},
			"key2": {Value: []byte("value2"), Timestamp: 2},
		},
		Clock: 2,
	}
	data, err := cbor.Marshal(payload)
	require.NoError(t, err)

	err = fsm.Restore(io.NopCloser(bytes.NewReader(data)))
	require.NoError(t, err)

	val, found := fsm.Get("key1")
	assert.Assert(t, found)
	assert.Equal(t, string(val), "value1")
}

type testSnapshotSinkComprehensive struct {
	buf bytes.Buffer
}

func (s *testSnapshotSinkComprehensive) ID() string                  { return "test-snapshot-id" }
func (s *testSnapshotSinkComprehensive) Write(p []byte) (int, error) { return s.buf.Write(p) }
func (s *testSnapshotSinkComprehensive) Close() error                { return nil }
func (s *testSnapshotSinkComprehensive) Cancel() error               { return nil }
