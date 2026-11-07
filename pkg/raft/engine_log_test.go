package raft

import (
	"context"
	"testing"

	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	"github.com/stretchr/testify/require"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

func etcdEntries(term uint64, from, to uint64) []*pb.Entry {
	var out []*pb.Entry
	for i := from; i <= to; i++ {
		out = append(out, &pb.Entry{Term: proto.Uint64(term), Index: proto.Uint64(i), Data: []byte{byte(i)}})
	}
	return out
}

func saved(t *testing.T, l *engineLog, snap *pb.Snapshot, hs *pb.HardState, ents []*pb.Entry) {
	t.Helper()
	ctx := context.Background()
	b, err := l.store.Batch(ctx)
	require.NoError(t, err)
	require.NoError(t, l.save(ctx, b, snap, hs, ents))
	require.NoError(t, b.Commit(ctx))
}

func TestALogReadsBackWhatItKept(t *testing.T) {
	ctx := context.Background()
	store := dssync.MutexWrap(ds.NewMapDatastore())
	l, err := openEngineLog(ctx, store, "/raft/g/engine")
	require.NoError(t, err)

	saved(t, l, nil, &pb.HardState{Term: proto.Uint64(2), Vote: proto.Uint64(1), Commit: proto.Uint64(5)}, etcdEntries(2, 1, 5))
	// A SHORTER TAIL replaces a longer one, on disk too.
	saved(t, l, nil, nil, etcdEntries(3, 3, 3))

	back, err := openEngineLog(ctx, store, "/raft/g/engine")
	require.NoError(t, err)
	last, _ := back.LastIndex()
	require.Equal(t, uint64(3), last, "entries 4 and 5 were replaced")
	term, err := back.Term(3)
	require.NoError(t, err)
	require.Equal(t, uint64(3), term)
	hs, _, err := back.InitialState()
	require.NoError(t, err)
	require.Equal(t, uint64(2), hs.GetTerm())

	// COMPACTED, it keeps a snapshot and drops the log before it but for what trails.
	cs := &pb.ConfState{Voters: []uint64{1}}
	require.NoError(t, back.compact(ctx, 3, cs, []byte("state"), 1))
	require.NoError(t, back.compact(ctx, 2, cs, []byte("older"), 1), "an older snapshot is no error, and changes nothing")
	again, err := openEngineLog(ctx, store, "/raft/g/engine")
	require.NoError(t, err)
	snap, err := again.Snapshot()
	require.NoError(t, err)
	require.Equal(t, uint64(3), snap.GetMetadata().GetIndex())
	require.Equal(t, "state", string(snap.GetData()))
	first, _ := again.FirstIndex()
	require.Equal(t, uint64(4), first)

	// A SNAPSHOT RAFT HANDS OVER is kept as it is.
	given := &pb.Snapshot{Data: []byte("given"), Metadata: &pb.SnapshotMetadata{Index: proto.Uint64(10), Term: proto.Uint64(4), ConfState: cs}}
	saved(t, again, given, nil, nil)
	fromSnap, err := openEngineLog(ctx, store, "/raft/g/engine")
	require.NoError(t, err)
	snap, _ = fromSnap.Snapshot()
	require.Equal(t, "given", string(snap.GetData()))

	require.NoError(t, fromSnap.wipe(ctx))
	empty, err := openEngineLog(ctx, store, "/raft/g/engine")
	require.NoError(t, err)
	last, _ = empty.LastIndex()
	require.Zero(t, last)
}

func TestALogThatCannotBeReadIsRefusedByName(t *testing.T) {
	ctx := context.Background()
	for name, key := range map[string]string{
		"snapshot":   "/raft/g/engine/snapshot",
		"hard state": "/raft/g/engine/hardstate",
		"entry":      "/raft/g/engine/log/00000000000000000001",
	} {
		t.Run(name, func(t *testing.T) {
			store := dssync.MutexWrap(ds.NewMapDatastore())
			require.NoError(t, store.Put(ctx, ds.NewKey(key), []byte{0xff, 0xff}))
			_, err := openEngineLog(ctx, store, "/raft/g/engine")
			require.ErrorContains(t, err, "raft: /raft/g/engine")
		})
	}
}
