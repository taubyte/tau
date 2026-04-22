package raft

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCRDTEntryWins(t *testing.T) {
	t.Run("higher_timestamp_wins", func(t *testing.T) {
		a := CRDTEntry{Timestamp: 5, WallClock: 100}
		b := CRDTEntry{Timestamp: 3, WallClock: 200}
		assert.True(t, crdtEntryWins(a, b))
		assert.False(t, crdtEntryWins(b, a))
	})

	t.Run("equal_timestamp_higher_wallclock_wins", func(t *testing.T) {
		a := CRDTEntry{Timestamp: 5, WallClock: 200}
		b := CRDTEntry{Timestamp: 5, WallClock: 100}
		assert.True(t, crdtEntryWins(a, b))
		assert.False(t, crdtEntryWins(b, a))
	})

	t.Run("equal_everything", func(t *testing.T) {
		a := CRDTEntry{Timestamp: 5, WallClock: 100}
		b := CRDTEntry{Timestamp: 5, WallClock: 100}
		assert.False(t, crdtEntryWins(a, b))
		assert.False(t, crdtEntryWins(b, a))
	})
}

func TestCommandMerge_LWW(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, "/raft/test")

	setCmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "key1", Value: []byte("local")},
	}
	data, _ := cbor.Marshal(setCmd)
	fsm.Apply(&raft.Log{Data: data, Index: 1})

	val, found := fsm.Get("key1")
	require.True(t, found)
	assert.Equal(t, "local", string(val))

	delta := map[string]CRDTEntry{
		"key1": {Value: []byte("foreign"), Timestamp: 10, WallClock: time.Now().UnixNano()},
		"key2": {Value: []byte("new"), Timestamp: 5, WallClock: time.Now().UnixNano()},
	}
	mergeData, err := encodeMergeCommand(delta)
	require.NoError(t, err)
	fsm.Apply(&raft.Log{Data: mergeData, Index: 2})

	val, found = fsm.Get("key1")
	require.True(t, found)
	assert.Equal(t, "foreign", string(val))

	val, found = fsm.Get("key2")
	require.True(t, found)
	assert.Equal(t, "new", string(val))
}

func TestCommandMerge_LowerTimestampLoses(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, "/raft/test")

	setCmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "key1", Value: []byte("local")},
	}
	data, _ := cbor.Marshal(setCmd)
	fsm.Apply(&raft.Log{Data: data, Index: 1})

	delta := map[string]CRDTEntry{
		"key1": {Value: []byte("foreign"), Timestamp: 0, WallClock: 0},
	}
	mergeData, err := encodeMergeCommand(delta)
	require.NoError(t, err)
	fsm.Apply(&raft.Log{Data: mergeData, Index: 2})

	val, found := fsm.Get("key1")
	require.True(t, found)
	assert.Equal(t, "local", string(val))
}

func TestCommandMerge_Tombstone(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, "/raft/test")

	setCmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "key1", Value: []byte("alive")},
	}
	data, _ := cbor.Marshal(setCmd)
	fsm.Apply(&raft.Log{Data: data, Index: 1})

	delta := map[string]CRDTEntry{
		"key1": {Deleted: true, Timestamp: 100, WallClock: time.Now().UnixNano()},
	}
	mergeData, err := encodeMergeCommand(delta)
	require.NoError(t, err)
	fsm.Apply(&raft.Log{Data: mergeData, Index: 2})

	_, found := fsm.Get("key1")
	assert.False(t, found)

	keys := fsm.Keys("")
	assert.Empty(t, keys)
}

func TestExportState(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, "/raft/test")

	for i, key := range []string{"a", "b", "c"} {
		cmd := Command{
			Type: CommandSet,
			Set:  &SetCommand{Key: key, Value: []byte("val-" + key)},
		}
		data, _ := cbor.Marshal(cmd)
		fsm.Apply(&raft.Log{Data: data, Index: uint64(i + 1)})
	}

	state, err := fsm.ExportState()
	require.NoError(t, err)
	assert.Len(t, state, 3)

	assert.Equal(t, "val-a", string(state["a"].Value))
	assert.Equal(t, uint64(1), state["a"].Timestamp)

	assert.Equal(t, "val-c", string(state["c"].Value))
	assert.Equal(t, uint64(3), state["c"].Timestamp)
}

func TestFSM_Delete_CreatesTombstone(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, "/raft/test")

	setCmd := Command{Type: CommandSet, Set: &SetCommand{Key: "k", Value: []byte("v")}}
	data, _ := cbor.Marshal(setCmd)
	fsm.Apply(&raft.Log{Data: data, Index: 1})

	delCmd := Command{Type: CommandDelete, Delete: &DeleteCommand{Key: "k"}}
	data, _ = cbor.Marshal(delCmd)
	fsm.Apply(&raft.Log{Data: data, Index: 2})

	_, found := fsm.Get("k")
	assert.False(t, found)

	state, err := fsm.ExportState()
	require.NoError(t, err)
	entry, exists := state["k"]
	assert.True(t, exists)
	assert.True(t, entry.Deleted)
	assert.Equal(t, uint64(2), entry.Timestamp)
}

func TestFSM_LamportClock_Increments(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, "/raft/test")

	for i := 0; i < 5; i++ {
		cmd := Command{
			Type: CommandSet,
			Set:  &SetCommand{Key: "k", Value: []byte("v")},
		}
		data, _ := cbor.Marshal(cmd)
		fsm.Apply(&raft.Log{Data: data, Index: uint64(i + 1)})
	}

	state, err := fsm.ExportState()
	require.NoError(t, err)

	assert.Equal(t, uint64(5), state["k"].Timestamp)
}

func TestFSM_SnapshotRestorePreservesClock(t *testing.T) {
	store1 := newTestStore()
	fsm1 := newKVFSM(t.Context(), store1, "/raft/test")

	for i := 0; i < 3; i++ {
		cmd := Command{
			Type: CommandSet,
			Set:  &SetCommand{Key: "k", Value: []byte("v")},
		}
		data, _ := cbor.Marshal(cmd)
		fsm1.Apply(&raft.Log{Data: data, Index: uint64(i + 1)})
	}

	snap, err := fsm1.Snapshot()
	require.NoError(t, err)

	var buf bytes.Buffer
	sink := &testSnapshotSink{Writer: &buf}
	require.NoError(t, snap.Persist(sink))

	store2 := newTestStore()
	fsm2 := newKVFSM(t.Context(), store2, "/raft/test")
	require.NoError(t, fsm2.Restore(io.NopCloser(bytes.NewReader(buf.Bytes()))))

	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "new", Value: []byte("v")},
	}
	data, _ := cbor.Marshal(cmd)
	fsm2.Apply(&raft.Log{Data: data, Index: 4})

	state, err := fsm2.ExportState()
	require.NoError(t, err)
	assert.Equal(t, uint64(4), state["new"].Timestamp)
}

func TestNegotiateWinner(t *testing.T) {
	t.Run("more_members_wins", func(t *testing.T) {
		a := &ClusterInfoResponse{NodeID: "A", MemberCount: 3, LastIndex: 10, LeaderID: "A"}
		b := &ClusterInfoResponse{NodeID: "B", MemberCount: 1, LastIndex: 10, LeaderID: "B"}
		assert.Equal(t, "A", negotiateWinner(a, b))
		assert.Equal(t, "A", negotiateWinner(b, a))
	})

	t.Run("equal_members_higher_index_wins", func(t *testing.T) {
		a := &ClusterInfoResponse{NodeID: "A", MemberCount: 2, LastIndex: 20, LeaderID: "A"}
		b := &ClusterInfoResponse{NodeID: "B", MemberCount: 2, LastIndex: 10, LeaderID: "B"}
		assert.Equal(t, "A", negotiateWinner(a, b))
		assert.Equal(t, "A", negotiateWinner(b, a))
	})

	t.Run("all_equal_lower_leader_wins", func(t *testing.T) {
		a := &ClusterInfoResponse{NodeID: "A", MemberCount: 2, LastIndex: 10, LeaderID: "AAA"}
		b := &ClusterInfoResponse{NodeID: "B", MemberCount: 2, LastIndex: 10, LeaderID: "BBB"}
		assert.Equal(t, "A", negotiateWinner(a, b))
		assert.Equal(t, "A", negotiateWinner(b, a))
	})

	t.Run("deterministic", func(t *testing.T) {
		a := &ClusterInfoResponse{NodeID: "node1", MemberCount: 1, LastIndex: 5, LeaderID: "node1"}
		b := &ClusterInfoResponse{NodeID: "node2", MemberCount: 1, LastIndex: 5, LeaderID: "node2"}
		w1 := negotiateWinner(a, b)
		w2 := negotiateWinner(b, a)
		assert.Equal(t, w1, w2, "must produce the same winner regardless of argument order")
	})
}

func TestCommandMerge_Empty(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, "/raft/test")

	mergeCmd := Command{
		Type:  CommandMerge,
		Merge: &MergeCommand{Delta: map[string]CRDTEntry{}},
	}
	data, _ := cbor.Marshal(mergeCmd)
	resp := fsm.Apply(&raft.Log{Data: data, Index: 1})
	fsmResp := resp.(FSMResponse)
	assert.Error(t, fsmResp.Error)
}

func TestCommandMerge_AdvancesClock(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, "/raft/test")

	delta := map[string]CRDTEntry{
		"k": {Value: []byte("v"), Timestamp: 42, WallClock: time.Now().UnixNano()},
	}
	mergeData, _ := encodeMergeCommand(delta)
	fsm.Apply(&raft.Log{Data: mergeData, Index: 1})

	cmd := Command{Type: CommandSet, Set: &SetCommand{Key: "k2", Value: []byte("v2")}}
	data, _ := cbor.Marshal(cmd)
	fsm.Apply(&raft.Log{Data: data, Index: 2})

	state, err := fsm.ExportState()
	require.NoError(t, err)
	assert.Greater(t, state["k2"].Timestamp, uint64(42))
}
