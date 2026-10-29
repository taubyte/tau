package mst

import (
	"bytes"
	"fmt"
	"testing"

	cid "github.com/ipfs/go-cid"
)

// wideNode is a node of 16 entries with a subtree on every other slot.
func wideNode(t testing.TB) *node {
	t.Helper()
	leaf, err := encode(&node{entries: []entry{{key: []byte("leaf"), val: []byte("v")}}, kids: make([]ref, 2)})
	if err != nil {
		t.Fatal(err)
	}
	n := &node{level: 1, kids: make([]ref, 17)}
	for i := range 16 {
		n.entries = append(n.entries, entry{key: fmt.Appendf(nil, "key/%04d", i), val: bytes.Repeat([]byte{byte(i)}, 32)})
	}
	for i := 0; i < len(n.kids); i += 2 {
		n.kids[i] = ref{c: leaf.Cid(), sum: []byte("sum")}
	}
	return n
}

// TestNodeCodecAllocations bounds what encoding and decoding a node costs:
// a decode allocates the node, its entries, its kids, one buffer for its
// keys and the copy of the links ProtoNode.Links makes, and copies none of
// the block's bytes; an encode allocates the data, the ProtoNode, its links,
// the copy of them SetLinks makes, and the hasher SetCidBuilder makes only to
// check it can (two).
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
	t.Logf("decode: %v allocations", a)
	if a > 5 {
		t.Errorf("decode: %v allocations, want at most 5", a)
	}
	a = testing.AllocsPerRun(200, func() {
		if _, err := encode(n); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("encode: %v allocations", a)
	if a > 6 {
		t.Errorf("encode: %v allocations, want at most 6", a)
	}
}

// TestDecodedBytesCannotReachTheBlock: a decoded value or summary shares
// the block's bytes, so appending to one must make a copy rather than write
// over the bytes that follow it in the block.
func TestDecodedBytesCannotReachTheBlock(t *testing.T) {
	nd, err := encode(wideNode(t))
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(nd.Data())
	n, err := decode(nd, noLevel)
	if err != nil {
		t.Fatal(err)
	}
	var grown [][]byte
	for i, e := range n.entries {
		grown = append(grown, append(e.val, 'x'), append(n.kids[i].sum, 'x'))
	}
	if !bytes.Equal(nd.Data(), before) {
		t.Fatal("an append to a decoded slice wrote into the block")
	}
	if len(grown) != 32 || n.kids[0].c == cid.Undef {
		t.Fatal("unexpected node shape")
	}
}
