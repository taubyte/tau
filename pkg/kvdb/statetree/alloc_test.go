package statetree

import (
	"bytes"
	"fmt"
	"testing"

	cid "github.com/ipfs/go-cid"
)

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

func TestANodeIsMadeInOneAllocation(t *testing.T) {
	for _, entries := range []int{1, 8, 9, 16, 17, 32, 33, 64, 65} {
		n := newNode(3, entries)
		if n.level != 3 || len(n.entries) != entries || len(n.kids) != entries+1 || cap(n.entries) != nodeRoom(entries) {
			t.Fatalf("%d entries: level %d, %d entries, %d kids, room for %d", entries, n.level, len(n.entries), len(n.kids), cap(n.entries))
		}
		want := 1.0
		if entries > 64 {
			want = 3
		}
		if a := testing.AllocsPerRun(100, func() { newNode(3, entries) }); a != want {
			t.Fatalf("%d entries: %v allocations, want %v", entries, a, want)
		}
	}
}
