//go:build !race

// sync.Pool drops entries at random under the race detector, so allocations
// are counted without it.

package statetree

import "testing"

// A decode allocates the node, its key buffer and its links, copying none of
// the block's bytes; an encode allocates the data, the block with its cid, and
// the Block.
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
