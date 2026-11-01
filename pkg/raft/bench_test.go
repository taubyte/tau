package raft

import (
	"context"
	"strconv"
	"testing"

	"github.com/hashicorp/raft"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
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
	log := &raft.Log{Data: data}
	b.ReportAllocs()
	for b.Loop() {
		log.Index++
		if r, ok := f.Apply(log).(FSMResponse); !ok || r.Error != nil {
			b.Fatal(r.Error)
		}
	}
}

func BenchmarkStoreLogs(b *testing.B) {
	l := newLogStore(dssync.MutexWrap(ds.NewMapDatastore()), "/raft/bench/log")
	data := make([]byte, 300)
	logs := make([]*raft.Log, 16)
	for i := range logs {
		logs[i] = &raft.Log{Type: raft.LogCommand, Term: 3, Data: data}
	}
	var index uint64
	b.ReportAllocs()
	for b.Loop() {
		for _, lg := range logs {
			index++
			lg.Index = index
		}
		if err := l.StoreLogs(logs); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGetLog(b *testing.B) {
	l := newLogStore(dssync.MutexWrap(ds.NewMapDatastore()), "/raft/bench/log")
	for i := uint64(1); i <= 64; i++ {
		if err := l.StoreLog(&raft.Log{Index: i, Term: 3, Type: raft.LogCommand, Data: []byte("payload-" + strconv.FormatUint(i, 10))}); err != nil {
			b.Fatal(err)
		}
	}
	var out raft.Log
	b.ReportAllocs()
	i := uint64(0)
	for b.Loop() {
		i = i%64 + 1
		if err := l.GetLog(i, &out); err != nil {
			b.Fatal(err)
		}
	}
}
