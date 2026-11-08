package raft

import (
	"context"
	"testing"

	ds "github.com/ipfs/go-datastore"
	"github.com/stretchr/testify/require"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

const faultLog = "/raft/g/engine"

// A LOG THAT CANNOT BE READ BACK IS NOT OPENED: every read it makes, and every
// record it reads, either comes back whole or refuses the group by name.
func TestALogThatCannotBeReadBackIsNotOpened(t *testing.T) {
	ctx := context.Background()
	kept := func(t *testing.T) *faultStore {
		store := newFaultStore()
		l, err := openEngineLog(ctx, store, faultLog)
		require.NoError(t, err)
		saved(t, l, &pb.Snapshot{Metadata: &pb.SnapshotMetadata{Index: proto.Uint64(2), Term: proto.Uint64(1)}},
			&pb.HardState{Term: proto.Uint64(1), Commit: proto.Uint64(3)}, etcdEntries(1, 3, 4))
		return store
	}
	for name, c := range map[string]struct {
		fault func(*faultStore)
		says  string
	}{
		"the snapshot is unreadable":   {func(s *faultStore) { s.failGet(faultLog+"/snapshot", errNoSpace) }, errNoSpace.Error()},
		"the snapshot is garbage":      {func(s *faultStore) { s.Batching.Put(ctx, ds.NewKey(faultLog+"/snapshot"), []byte{0xff}) }, "reading its snapshot"},
		"the hard state is unreadable": {func(s *faultStore) { s.failGet(faultLog+"/hardstate", errNoSpace) }, errNoSpace.Error()},
		"the hard state is garbage":    {func(s *faultStore) { s.Batching.Put(ctx, ds.NewKey(faultLog+"/hardstate"), []byte{0xff}) }, "reading its hard state"},
		"the log cannot be listed":     {func(s *faultStore) { s.failQuery(faultLog+"/log", errNoSpace) }, errNoSpace.Error()},
		"the log breaks off":           {func(s *faultStore) { s.failScan(faultLog+"/log", errNoSpace) }, errNoSpace.Error()},
		"an entry is garbage": {func(s *faultStore) {
			s.Batching.Put(ctx, ds.NewKey(faultLog+"/log/00000000000000000004"), []byte{0xff})
		}, "reading entry"},
	} {
		t.Run(name, func(t *testing.T) {
			store := kept(t)
			c.fault(store)
			_, err := openEngineLog(ctx, store, faultLog)
			require.ErrorContains(t, err, c.says)
		})
	}
}

// A SAVE THE BATCH REFUSES leaves memory where it was, so the same save is made
// again once the store takes it.
func TestASaveTheBatchRefusesLeavesMemoryAlone(t *testing.T) {
	ctx := context.Background()
	snap := &pb.Snapshot{Metadata: &pb.SnapshotMetadata{Index: proto.Uint64(2), Term: proto.Uint64(1)}}
	hs := &pb.HardState{Term: proto.Uint64(1), Commit: proto.Uint64(3)}
	for name, key := range map[string]string{
		"its snapshot":   faultLog + "/snapshot",
		"an entry":       faultLog + "/log/00000000000000000003",
		"its hard state": faultLog + "/hardstate",
	} {
		t.Run(name, func(t *testing.T) {
			store := newFaultStore()
			l, err := openEngineLog(ctx, store, faultLog)
			require.NoError(t, err)
			store.failStaging(key, errNoSpace)
			b, err := store.Batch(ctx)
			require.NoError(t, err)
			require.ErrorIs(t, l.save(ctx, b, snap, hs, etcdEntries(1, 3, 3)), errNoSpace)
			last, _ := l.LastIndex()
			require.Zero(t, last, "nothing reached memory")

			store.failStaging(key, nil)
			saved(t, l, snap, hs, etcdEntries(1, 3, 3))
			last, _ = l.LastIndex()
			require.Equal(t, uint64(3), last)
		})
	}

	t.Run("dropping a replaced tail", func(t *testing.T) {
		store := newFaultStore()
		l, err := openEngineLog(ctx, store, faultLog)
		require.NoError(t, err)
		saved(t, l, nil, nil, etcdEntries(1, 1, 3))
		store.failStaging(faultLog+"/log/00000000000000000003", errNoSpace)
		b, err := store.Batch(ctx)
		require.NoError(t, err)
		require.ErrorIs(t, l.save(ctx, b, nil, nil, etcdEntries(2, 2, 2)), errNoSpace)
	})

	t.Run("an older snapshot", func(t *testing.T) {
		store := newFaultStore()
		l, err := openEngineLog(ctx, store, faultLog)
		require.NoError(t, err)
		saved(t, l, &pb.Snapshot{Metadata: &pb.SnapshotMetadata{Index: proto.Uint64(5), Term: proto.Uint64(1)}}, nil, nil)
		require.NoError(t, l.kept(snap, nil, nil), "a snapshot older than memory's is no news")
	})
}

// A COMPACTION THE STORE REFUSES says so, and one already behind the log is a
// no-op rather than a refusal.
func TestACompactionTheStoreRefusesSaysSo(t *testing.T) {
	ctx := context.Background()
	cs := &pb.ConfState{Voters: []uint64{1}}
	logged := func(t *testing.T) (*faultStore, *engineLog) {
		store := newFaultStore()
		l, err := openEngineLog(ctx, store, faultLog)
		require.NoError(t, err)
		saved(t, l, nil, &pb.HardState{Term: proto.Uint64(1), Commit: proto.Uint64(10)}, etcdEntries(1, 1, 10))
		return store, l
	}

	t.Run("writing the snapshot", func(t *testing.T) {
		store, l := logged(t)
		store.failPut(faultLog+"/snapshot", errNoSpace)
		require.ErrorIs(t, l.compact(ctx, 8, cs, nil, 2), errNoSpace)
	})
	t.Run("opening the batch", func(t *testing.T) {
		store, l := logged(t)
		store.failBatches(errNoSpace)
		require.ErrorIs(t, l.compact(ctx, 8, cs, nil, 2), errNoSpace)
	})
	t.Run("dropping an entry", func(t *testing.T) {
		store, l := logged(t)
		store.failStaging(faultLog+"/log/00000000000000000003", errNoSpace)
		require.ErrorIs(t, l.compact(ctx, 8, cs, nil, 2), errNoSpace)
	})
	t.Run("a snapshot already taken", func(t *testing.T) {
		_, l := logged(t)
		require.NoError(t, l.compact(ctx, 8, cs, nil, 2))
		require.NoError(t, l.compact(ctx, 6, cs, nil, 2), "behind the last snapshot is nothing to do")
	})
}

// A WIPE THE STORE REFUSES says so rather than leave half a group behind quietly.
func TestAWipeTheStoreRefusesSaysSo(t *testing.T) {
	ctx := context.Background()
	for name, fault := range map[string]func(*faultStore){
		"listing":  func(s *faultStore) { s.failQuery(faultLog, errNoSpace) },
		"scanning": func(s *faultStore) { s.failScan(faultLog, errNoSpace) },
		"batching": func(s *faultStore) { s.failBatches(errNoSpace) },
		"deleting": func(s *faultStore) { s.failStaging(faultLog+"/hardstate", errNoSpace) },
	} {
		t.Run(name, func(t *testing.T) {
			store := newFaultStore()
			l, err := openEngineLog(ctx, store, faultLog)
			require.NoError(t, err)
			saved(t, l, nil, &pb.HardState{Term: proto.Uint64(1)}, etcdEntries(1, 1, 2))
			fault(store)
			require.ErrorIs(t, l.wipe(ctx), errNoSpace)
		})
	}
}
