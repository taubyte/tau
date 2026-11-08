package raft

import (
	"context"
	"testing"

	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

func BenchmarkEncodeSetCommand(b *testing.B) {
	value := make([]byte, 256)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := encodeSetCommand("orders/2026/11/01/abc", value); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFSMApplySet(b *testing.B) {
	f := newKVFSM(context.Background(), dssync.MutexWrap(ds.NewMapDatastore()), "/raft/bench")
	data, err := encodeSetCommand("orders/2026/11/01/abc", make([]byte, 256))
	if err != nil {
		b.Fatal(err)
	}
	var index uint64
	b.ReportAllocs()
	for b.Loop() {
		index++
		if r := f.Apply(index, data); r.Error != nil {
			b.Fatal(r.Error)
		}
	}
}

// BenchmarkSaveEntries is one cycle's write of sixteen entries to a group's log.
func BenchmarkSaveEntries(b *testing.B) {
	ctx := context.Background()
	store := dssync.MutexWrap(ds.NewMapDatastore())
	l, err := openEngineLog(ctx, store, "/raft/bench/engine")
	if err != nil {
		b.Fatal(err)
	}
	data := make([]byte, 300)
	var index uint64
	b.ReportAllocs()
	for b.Loop() {
		ents := make([]*pb.Entry, 16)
		for i := range ents {
			index++
			ents[i] = &pb.Entry{Term: proto.Uint64(3), Index: proto.Uint64(index), Data: data}
		}
		batch, err := store.Batch(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if err := l.save(ctx, batch, nil, nil, ents); err != nil {
			b.Fatal(err)
		}
		if err := batch.Commit(ctx); err != nil {
			b.Fatal(err)
		}
		if err := l.kept(nil, nil, ents); err != nil {
			b.Fatal(err)
		}
	}
}
