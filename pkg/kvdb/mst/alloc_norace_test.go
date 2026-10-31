//go:build !race

// The race detector makes sync.Pool drop what is put in it at random, so
// what a pooled message saves is counted only without it.

package mst

import "testing"

// TestNodeCodecAllocations bounds what encoding and decoding a node costs:
// a decode allocates the node, its entries, its kids, one buffer for its
// keys and the slice of its links, and copies none of the block's bytes, a
// link's cid among them; an encode allocates the data, the block's bytes,
// its cid and the Block.
func TestNodeCodecAllocations(t *testing.T) {
	n := wideNode(t)
	nd, err := encode(n)
	if err != nil {
		t.Fatal(err)
	}
	a := testing.AllocsPerRun(200, func() {
		if _, err := decode(nd, noLevel); err != nil {
			t.Fatal(err)
		}
	})
	_, links, err := DecodeBlock(nd, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("decode: %v allocations, %d links", a, len(links))
	if a > 5 {
		t.Errorf("decode: %v allocations, want at most 5", a)
	}
	a = testing.AllocsPerRun(200, func() {
		if _, err := encode(n); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("encode: %v allocations", a)
	if a > 4 {
		t.Errorf("encode: %v allocations, want at most 4", a)
	}
}
