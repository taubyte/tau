//go:build !race

// The race detector makes sync.Pool drop what is put in it at random, so
// what a pooled message saves is counted only without it.

package statetree

import "testing"

// TestNodeCodecAllocations bounds what encoding and decoding a node costs:
// a decode allocates the node with its entries and kids, one buffer for
// its keys and the slice of its links, and copies none of the block's bytes, a
// link's cid among them; an encode allocates the data, the block's bytes
// with its cid, and the Block.
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
	if a > 3 {
		t.Errorf("decode: %v allocations, want at most 3", a)
	}
	a = testing.AllocsPerRun(200, func() {
		if _, err := encode(n); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("encode: %v allocations", a)
	if a > 3 {
		t.Errorf("encode: %v allocations, want at most 3", a)
	}
}

// TestABlockInItsBufferIsOneAllocation: the Block; its cid is in the
// buffer.
func TestABlockInItsBufferIsOneAllocation(t *testing.T) {
	want, err := NewBlock([]byte("data"), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := BlockBuffer(len(want.RawData()))
	copy(raw, want.RawData())
	if a := testing.AllocsPerRun(100, func() {
		if _, err := BlockInBuffer(raw); err != nil {
			t.Fatal(err)
		}
	}); a != 1 {
		t.Fatalf("a block in its buffer: %v allocations, want 1", a)
	}
}
