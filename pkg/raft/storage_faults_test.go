package raft

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/require"
)

const logFaultsPrefix = "/raft/test/log"

func logKeyOf(index uint64) string { return fmt.Sprintf("%s/%020d", logFaultsPrefix, index) }

func storedLogs(t *testing.T, logs *datastoreLogStore, indexes ...uint64) {
	t.Helper()
	for _, index := range indexes {
		require.NoError(t, logs.StoreLog(&raft.Log{Index: index, Term: 1, Data: []byte{byte(index)}}))
	}
}

// TestLogStore_ALogTheStoreCannotReadIsNotAnEmptyOne: raft takes index zero
// for an empty log and a missing entry for one compacted away; a disk that
// failed is neither, and answering as if it were sends raft down the path for
// a log that has nothing in it.
func TestLogStore_ALogTheStoreCannotReadIsNotAnEmptyOne(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault func(*faultStore, error)
	}{
		{"refused query", func(s *faultStore, err error) { s.failQuery(logFaultsPrefix, err) }},
		{"scan that fails partway", func(s *faultStore, err error) { s.failScan(logFaultsPrefix, err) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFaultStore()
			logs := newLogStore(store, logFaultsPrefix)
			storedLogs(t, logs, 1, 2, 3)
			tc.fault(store, errDisk)

			first, err := logs.FirstIndex()
			require.ErrorIs(t, err, errDisk)
			require.Zero(t, first)

			last, err := logs.LastIndex()
			require.ErrorIs(t, err, errDisk)
			require.Zero(t, last)
		})
	}

	store := newFaultStore()
	logs := newLogStore(store, logFaultsPrefix)
	storedLogs(t, logs, 1)
	store.failGet(logKeyOf(1), errDisk)

	var entry raft.Log
	err := logs.GetLog(1, &entry)
	require.ErrorIs(t, err, errDisk)
	require.NotErrorIs(t, err, raft.ErrLogNotFound, "an entry the disk could not read answered as one compacted away")
}

// TestLogStore_AWriteTheStoreRefusesStoresNothing: raft appends a run of
// entries at once and takes an error to mean none of them is there.
func TestLogStore_AWriteTheStoreRefusesStoresNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault func(*faultStore, error)
	}{
		{"store that will not open a batch", func(s *faultStore, err error) { s.failBatches(err) }},
		{"entry that will not stage", func(s *faultStore, err error) { s.failStaging(logKeyOf(2), err) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFaultStore()
			logs := newLogStore(store, logFaultsPrefix)
			tc.fault(store, errNoSpace)

			err := logs.StoreLogs([]*raft.Log{
				{Index: 1, Term: 1, Data: []byte("a")},
				{Index: 2, Term: 1, Data: []byte("b")},
			})
			require.ErrorIs(t, err, errNoSpace)

			var entry raft.Log
			require.ErrorIs(t, logs.GetLog(1, &entry), raft.ErrLogNotFound, "part of a refused append landed")
		})
	}
}

// TestLogStore_ADeleteTheStoreRefusesDeletesNothing: raft deletes a range to
// compact or to truncate a conflicting suffix, and an error leaves the whole
// range where it was.
func TestLogStore_ADeleteTheStoreRefusesDeletesNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault func(*faultStore, error)
	}{
		{"store that will not open a batch", func(s *faultStore, err error) { s.failBatches(err) }},
		{"entry that will not stage", func(s *faultStore, err error) { s.failStaging(logKeyOf(3), err) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFaultStore()
			logs := newLogStore(store, logFaultsPrefix)
			storedLogs(t, logs, 1, 2, 3, 4)
			tc.fault(store, errNoSpace)

			require.ErrorIs(t, logs.DeleteRange(2, 3), errNoSpace)
			for _, index := range []uint64{1, 2, 3, 4} {
				var entry raft.Log
				require.NoError(t, logs.GetLog(index, &entry), "entry %d went with a refused delete", index)
			}
		})
	}
}

// TestStableStore_AValueTheStoreCannotReadIsNotAZero: raft keeps its term and
// its vote here, and reads an absent one as zero. A disk error read the same
// way is a node that forgot it already voted in this term, and votes again.
func TestStableStore_AValueTheStoreCannotReadIsNotAZero(t *testing.T) {
	store := newFaultStore()
	stable := newStableStore(store, "/raft/test/stable")
	require.NoError(t, stable.SetUint64([]byte("CurrentTerm"), 7))
	store.failGet("/raft/test/stable/CurrentTerm", errDisk)

	value, err := stable.Get([]byte("CurrentTerm"))
	require.ErrorIs(t, err, errDisk)
	require.Nil(t, value)

	term, err := stable.GetUint64([]byte("CurrentTerm"))
	require.ErrorIs(t, err, errDisk)
	require.Zero(t, term)
}

func newSnapshotStoreIn(t *testing.T) (*fileSnapshotStore, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "snapshots")
	snaps, err := newSnapshotStore(dir, 3)
	require.NoError(t, err)
	return snaps, dir
}

func writtenSnapshot(t *testing.T, snaps *fileSnapshotStore, index uint64) string {
	t.Helper()
	sink, err := snaps.Create(raft.SnapshotVersionMax, index, 1, raft.Configuration{}, 1, nil)
	require.NoError(t, err)
	_, err = sink.Write([]byte("state"))
	require.NoError(t, err)
	require.NoError(t, sink.Close())
	return sink.ID()
}

// TestSnapshotStore_ADirectoryThatIsNotThereOrNotADirectory: a store whose
// directory is gone has no snapshots, raft's answer for a node starting fresh;
// one whose directory is something else cannot say what it holds, and says so
// rather than claiming nothing.
func TestSnapshotStore_ADirectoryThatIsNotThereOrNotADirectory(t *testing.T) {
	snaps, dir := newSnapshotStoreIn(t)
	writtenSnapshot(t, snaps, 5)

	require.NoError(t, os.RemoveAll(dir))
	listed, err := snaps.List()
	require.NoError(t, err)
	require.Empty(t, listed)

	require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o644))
	listed, err = snaps.List()
	require.Error(t, err)
	require.Nil(t, listed)

	_, err = snaps.Create(raft.SnapshotVersionMax, 6, 1, raft.Configuration{}, 1, nil)
	require.Error(t, err, "a snapshot was begun where none can be written")
}

// TestSnapshotStore_ListsOnlySnapshots: what else sits in the directory is not
// a snapshot raft may restore from.
func TestSnapshotStore_ListsOnlySnapshots(t *testing.T) {
	snaps, dir := newSnapshotStoreIn(t)
	id := writtenSnapshot(t, snaps, 5)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stray"), []byte("x"), 0o644))

	listed, err := snaps.List()
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, id, listed[0].ID)
}

// TestSnapshotStore_ADamagedSnapshotDoesNotOpen: raft restores what Open hands
// it, so a snapshot whose metadata will not decode, or whose state is missing,
// is an error and never an empty state.
func TestSnapshotStore_ADamagedSnapshotDoesNotOpen(t *testing.T) {
	snaps, dir := newSnapshotStoreIn(t)

	corrupt := writtenSnapshot(t, snaps, 5)
	require.NoError(t, os.WriteFile(filepath.Join(dir, corrupt, "meta.cbor"), []byte{0xff, 0x00}, 0o644))
	meta, state, err := snaps.Open(corrupt)
	require.Error(t, err)
	require.Nil(t, meta)
	require.Nil(t, state)

	stateless := writtenSnapshot(t, snaps, 6)
	require.NoError(t, os.Remove(filepath.Join(dir, stateless, "state.bin")))
	meta, state, err = snaps.Open(stateless)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Nil(t, meta)
	require.Nil(t, state)
}

// TestSnapshotSink_ASnapshotThatCannotBeWrittenIsNotTaken: raft compacts its
// log once a sink closes cleanly, so a close that could not write the state or
// its metadata has to fail.
func TestSnapshotSink_ASnapshotThatCannotBeWrittenIsNotTaken(t *testing.T) {
	t.Run("state", func(t *testing.T) {
		snaps, dir := newSnapshotStoreIn(t)
		sink, err := snaps.Create(raft.SnapshotVersionMax, 5, 1, raft.Configuration{}, 1, nil)
		require.NoError(t, err)
		_, err = sink.Write([]byte("state"))
		require.NoError(t, err)
		require.NoError(t, os.RemoveAll(filepath.Join(dir, sink.ID())))

		require.ErrorIs(t, sink.Close(), os.ErrNotExist)
		listed, err := snaps.List()
		require.NoError(t, err)
		require.Empty(t, listed)
	})

	t.Run("metadata", func(t *testing.T) {
		snaps, dir := newSnapshotStoreIn(t)
		sink, err := snaps.Create(raft.SnapshotVersionMax, 5, 1, raft.Configuration{}, 1, nil)
		require.NoError(t, err)
		require.NoError(t, os.Mkdir(filepath.Join(dir, sink.ID(), "meta.cbor"), 0o755))

		require.Error(t, sink.Close())
		listed, err := snaps.List()
		require.NoError(t, err)
		require.Empty(t, listed, "a snapshot without its metadata was listed")
	})
}
