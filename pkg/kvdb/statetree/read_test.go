package statetree

import (
	"context"
	"maps"
	"sync/atomic"
	"testing"

	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
)

type reading struct {
	*counting
	reads atomic.Int64
}

func (r *reading) ReadBlock(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	r.reads.Add(1)
	return r.bs.Get(ctx, c)
}

func TestATreeReadsThroughABlockReader(t *testing.T) {
	s := newStore()
	m := map[string][]byte{}
	for i := range 500 {
		m[string(key(i))] = value(i)
	}
	root := build(t, s, m).Root()
	for _, opts := range [][]Option{nil, {WithCache(NewCache(1 << 20))}} {
		r := &reading{counting: s}
		s.reset()
		tr := Load(r, root, opts...)
		if got := entries(t, tr); !maps.EqualFunc(got, m, func(a, b []byte) bool { return string(a) == string(b) }) {
			t.Fatalf("read %d entries through a block reader, want %d", len(got), len(m))
		}
		verify(t, tr)
		if s.gets.Load() != 0 || r.reads.Load() == 0 {
			t.Fatalf("%d nodes decoded by Get and %d blocks read: a block reader reads every node", s.gets.Load(), r.reads.Load())
		}
	}
}
