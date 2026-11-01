//go:build !race

// sync.Pool drops entries at random under the race detector, so allocations
// are counted without it.

package kvdb

import (
	"context"
	"testing"

	ipld "github.com/ipfs/go-ipld-format"
)

// A link's cid aliases the block's bytes.
func TestReceivingABlockAllocatesNothing(t *testing.T) {
	ctx := context.Background()
	replicas, _, done := makeNReplicasSeparateStores(t, 1, noAutoCompact())
	defer done()
	a := replicas[0]
	seedLevels(t, a, fan(16)...)
	compact(t, a)
	var nd ipld.Node
	for _, level := range stateLevels(t, a) {
		for _, n := range level {
			if nd == nil || len(blockLinksOf(t, n)) > len(blockLinksOf(t, nd)) {
				nd = n
			}
		}
	}
	links := len(blockLinksOf(t, nd))
	if links < 8 {
		t.Fatalf("no block of the state links more than %d", links)
	}

	f := &stateFetch{b: takeFetchBuffers()}
	defer f.b.putBack()
	receive := func() {
		f.b.asked[keyOf(nd.Cid())] = struct{}{}
		if !f.b.received(nd) {
			t.Fatal("a block asked for was not taken")
		}
		if err := a.discover(ctx, f, f.b.linked); err != nil {
			t.Fatal(err)
		}
		f.b.sess.reset()
		f.b.linked, f.b.parents = f.b.linked[:0], f.b.parents[:0]
	}
	receive()
	if n := testing.AllocsPerRun(100, receive); n > 0 {
		t.Fatalf("receiving a block of %d links allocates %.1f times", links, n)
	}
}
