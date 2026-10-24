package raft

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/hashicorp/raft"
	ds "github.com/ipfs/go-datastore"
	"github.com/stretchr/testify/require"
)

var errNoSpace = errors.New("pebble error during commit: no space left on device")

// timestampOf reads back the Lamport timestamp the FSM stored for key.
func timestampOf(t *testing.T, store ds.Batching, key string) uint64 {
	t.Helper()
	raw, err := store.Get(t.Context(), ds.NewKey(faultPrefix+"/data/"+key))
	require.NoError(t, err)
	var entry CRDTEntry
	require.NoError(t, cbor.Unmarshal(raw, &entry))
	return entry.Timestamp
}

// TestAMergeThatCannotLandWritesNoneOfIt: a merge is the delta of a group
// that is about to wipe itself, and raft never offers an applied entry again.
// Written key by key, a merge the store refused partway left the keys that map
// order happened to reach first, and moved the clock to theirs.
func TestAMergeThatCannotLandWritesNoneOfIt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault func(*faultStore, string, error)
	}{
		{"refused at commit", (*faultStore).failPut},
		{"refused while staging", (*faultStore).failStaging},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFaultStore()
			fsm := newKVFSM(t.Context(), store, faultPrefix)

			delta := make(map[string]CRDTEntry, 64)
			for i := range 64 {
				delta[fmt.Sprintf("k%02d", i)] = CRDTEntry{Value: []byte("v"), Timestamp: uint64(100 + i), WallClock: 1}
			}
			tc.fault(store, faultPrefix+"/data/k31", errNoSpace)

			resp := applied(t, fsm, Command{Type: CommandMerge, Merge: &MergeCommand{Delta: delta}})
			require.ErrorIs(t, resp.Error, errNoSpace)
			for key := range delta {
				require.True(t, absent(t, store, faultPrefix, key), "%q landed from a merge that did not", key)
			}

			require.Nil(t, applied(t, fsm, Command{Type: CommandSet, Set: &SetCommand{Key: "after", Value: []byte("1")}}).Error)
			require.Equal(t, uint64(1), timestampOf(t, store, "after"), "the refused merge moved the clock")
		})
	}
}

// TestAMergeTheStoreWillNotOpenWritesNothing: the batch a merge lands in is
// refused before anything is staged.
func TestAMergeTheStoreWillNotOpenWritesNothing(t *testing.T) {
	store := newFaultStore()
	fsm := newKVFSM(t.Context(), store, faultPrefix)
	store.failBatches(errNoSpace)

	resp := applied(t, fsm, Command{Type: CommandMerge, Merge: &MergeCommand{Delta: map[string]CRDTEntry{
		"a": {Value: []byte("1"), Timestamp: 7},
	}}})
	require.ErrorIs(t, resp.Error, errNoSpace)
	require.True(t, absent(t, store, faultPrefix, "a"))
}

// TestAMergeKeepsWhatItLoses: a merge replaces an entry only with one that
// wins against it, and the clock follows the entries that landed.
func TestAMergeKeepsWhatItLoses(t *testing.T) {
	store := newFaultStore()
	fsm := newKVFSM(t.Context(), store, faultPrefix)
	for range 5 {
		require.Nil(t, applied(t, fsm, Command{Type: CommandSet, Set: &SetCommand{Key: "kept", Value: []byte("ours")}}).Error)
	}

	resp := applied(t, fsm, Command{Type: CommandMerge, Merge: &MergeCommand{Delta: map[string]CRDTEntry{
		"kept":  {Value: []byte("theirs"), Timestamp: 2},
		"taken": {Value: []byte("theirs"), Timestamp: 9},
	}}})
	require.Nil(t, resp.Error)

	value, found, err := fsm.TryGet("kept")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("ours"), value)
	value, found, err = fsm.TryGet("taken")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("theirs"), value)

	require.Nil(t, applied(t, fsm, Command{Type: CommandSet, Set: &SetCommand{Key: "after", Value: []byte("1")}}).Error)
	require.Equal(t, uint64(10), timestampOf(t, store, "after"))
}

// TestABatchTheStoreWillNotStageWritesNoneOfIt: a staging write that fails
// abandons the batch before its commit, and leaves the clock where it was.
func TestABatchTheStoreWillNotStageWritesNoneOfIt(t *testing.T) {
	store := newFaultStore()
	fsm := newKVFSM(t.Context(), store, faultPrefix)
	store.failStaging(faultPrefix+"/data/b", errNoSpace)

	resp := applied(t, fsm, Command{
		Type: CommandBatch,
		Batch: []Command{
			{Type: CommandSet, Set: &SetCommand{Key: "a", Value: []byte("1")}},
			{Type: CommandSet, Set: &SetCommand{Key: "b", Value: []byte("2")}},
		},
	})
	require.ErrorIs(t, resp.Error, errNoSpace)
	require.True(t, absent(t, store, faultPrefix, "a"))

	require.Nil(t, applied(t, fsm, Command{Type: CommandSet, Set: &SetCommand{Key: "after", Value: []byte("1")}}).Error)
	require.Equal(t, uint64(1), timestampOf(t, store, "after"))
}

// TestABatchTheStoreWillNotOpenWritesNothing: a store that will not open a
// batch refuses the whole of it.
func TestABatchTheStoreWillNotOpenWritesNothing(t *testing.T) {
	store := newFaultStore()
	fsm := newKVFSM(t.Context(), store, faultPrefix)
	store.failBatches(errNoSpace)

	resp := applied(t, fsm, Command{
		Type:  CommandBatch,
		Batch: []Command{{Type: CommandSet, Set: &SetCommand{Key: "a", Value: []byte("1")}}},
	})
	require.ErrorIs(t, resp.Error, errNoSpace)
	require.True(t, absent(t, store, faultPrefix, "a"))
}

// TestASnapshotOfAStoreThatCannotBeReadFails: raft compacts the log behind
// every snapshot it takes, so a snapshot of what the store happened to return
// before it failed is a snapshot that has forgotten the rest for good.
func TestASnapshotOfAStoreThatCannotBeReadFails(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault func(*faultStore, error)
	}{
		{"refused query", func(s *faultStore, err error) { s.failQuery(faultPrefix+"/data", err) }},
		{"scan that fails partway", func(s *faultStore, err error) { s.failScan(faultPrefix+"/data", err) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFaultStore()
			fsm := newKVFSM(t.Context(), store, faultPrefix)
			for _, key := range []string{"a", "b"} {
				require.Nil(t, applied(t, fsm, Command{Type: CommandSet, Set: &SetCommand{Key: key, Value: []byte(key)}}).Error)
			}
			tc.fault(store, errDisk)

			snap, err := fsm.Snapshot()
			require.ErrorIs(t, err, errDisk)
			require.Nil(t, snap)

			state, err := fsm.ExportState()
			require.ErrorIs(t, err, errDisk, "an export the store cut short is not the state")
			require.Nil(t, state)
		})
	}
}

// failingSink is a snapshot sink whose writes fail, recording how the
// snapshot finished.
type failingSink struct {
	err               error
	closed, cancelled bool
}

func (s *failingSink) ID() string                  { return "failing" }
func (s *failingSink) Write(p []byte) (int, error) { return 0, s.err }
func (s *failingSink) Close() error                { s.closed = true; return nil }
func (s *failingSink) Cancel() error               { s.cancelled = true; return nil }

// TestASnapshotThatCannotBeWrittenIsCancelled: a sink closed after a failed
// write is a snapshot raft would keep, and restore from, holding part of a
// state.
func TestASnapshotThatCannotBeWrittenIsCancelled(t *testing.T) {
	fsm := newKVFSM(t.Context(), newFaultStore(), faultPrefix)
	require.Nil(t, applied(t, fsm, Command{Type: CommandSet, Set: &SetCommand{Key: "a", Value: []byte("1")}}).Error)

	snap, err := fsm.Snapshot()
	require.NoError(t, err)
	defer snap.Release()

	sink := &failingSink{err: errNoSpace}
	require.ErrorIs(t, snap.Persist(sink), errNoSpace)
	require.True(t, sink.cancelled, "a failed snapshot was not cancelled")
	require.False(t, sink.closed, "a failed snapshot was closed as if it were whole")
}

// snapshotOf encodes a snapshot holding data, the way Persist writes one.
func snapshotOf(t *testing.T, data map[string]CRDTEntry, clock uint64) io.ReadCloser {
	t.Helper()
	raw, err := cbor.Marshal(snapshotPayload{Data: data, Clock: clock})
	require.NoError(t, err)
	return io.NopCloser(bytes.NewReader(raw))
}

// TestARestoreTheStoreRefusesSaysSo: raft takes a failed Restore to mean the
// state machine does not hold the snapshot, and installs it again; one that
// reported success over a store that refused it would serve whatever was left.
func TestARestoreTheStoreRefusesSaysSo(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault func(*faultStore, error)
	}{
		{"refused query", func(s *faultStore, err error) { s.failQuery(faultPrefix+"/data", err) }},
		{"scan that fails partway", func(s *faultStore, err error) { s.failScan(faultPrefix+"/data", err) }},
		{"refused delete", func(s *faultStore, err error) { s.failDelete(faultPrefix+"/data/old", err) }},
		{"refused write", func(s *faultStore, err error) { s.failPut(faultPrefix+"/data/new", err) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFaultStore()
			fsm := newKVFSM(t.Context(), store, faultPrefix)
			require.Nil(t, applied(t, fsm, Command{Type: CommandSet, Set: &SetCommand{Key: "old", Value: []byte("1")}}).Error)
			tc.fault(store, errDisk)

			err := fsm.Restore(snapshotOf(t, map[string]CRDTEntry{"new": {Value: []byte("2"), Timestamp: 4}}, 4))
			require.ErrorIs(t, err, errDisk)
		})
	}
}

// TestARestoreReplacesTheState: what the store held before a snapshot is gone
// after it, and the clock is the snapshot's.
func TestARestoreReplacesTheState(t *testing.T) {
	store := newFaultStore()
	fsm := newKVFSM(t.Context(), store, faultPrefix)
	require.Nil(t, applied(t, fsm, Command{Type: CommandSet, Set: &SetCommand{Key: "old", Value: []byte("1")}}).Error)

	require.NoError(t, fsm.Restore(snapshotOf(t, map[string]CRDTEntry{"new": {Value: []byte("2"), Timestamp: 4}}, 4)))
	require.True(t, absent(t, store, faultPrefix, "old"))
	value, found, err := fsm.TryGet("new")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("2"), value)

	require.Nil(t, applied(t, fsm, Command{Type: CommandSet, Set: &SetCommand{Key: "after", Value: []byte("3")}}).Error)
	require.Equal(t, uint64(5), timestampOf(t, store, "after"))
}

var _ raft.SnapshotSink = (*failingSink)(nil)
