package kvdb

import (
	"fmt"
	"testing"

	dshelp "github.com/ipfs/boxo/datastore/dshelp"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	mh "github.com/multiformats/go-multihash"
)

// TestKeysAreTheirSegments: a key built in place is the key the segment
// functions spell, and costs one allocation.
func TestKeysAreTheirSegments(t *testing.T) {
	ns := ds.NewKey("/ns")
	s := newSet(nil, ns.ChildString(setNs))
	pre := newPrefixes(ns)
	d := dot{replica: "replica\x00\xff", counter: 42}
	h, err := mh.Sum([]byte("block"), mh.SHA2_256, -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []cid.Cid{cid.NewCidV0(h), cid.NewCidV1(cid.DagProtobuf, h), cid.Undef} {
		if got, want := hashKey(pre.blocks, c), ds.NewKey(pre.blocks).Child(dshelp.MultihashToDsKey(c.Hash())); got != want {
			t.Fatalf("hashKey %s, want %s", got, want)
		}
	}
	c := cid.NewCidV1(cid.DagProtobuf, h)
	for _, k := range []string{"", "/a", "/a/b", "x\x00y"} {
		cases := []struct {
			got  func() ds.Key
			want ds.Key
		}{
			{func() ds.Key { return s.keyElemsPrefix(k) }, ds.NewKey(s.elems).ChildString(keySegment(k))},
			{func() ds.Key { return s.elemKey(k, d) }, ds.NewKey(s.elems).ChildString(keySegment(k)).ChildString(d.segment())},
			{func() ds.Key { return s.pendingKey(k, d) }, ds.NewKey(s.pending).ChildString(d.segment()).ChildString(keySegment(k))},
			{func() ds.Key { return dotKey(pre.missing, d) }, ds.NewKey(pre.missing).ChildString(d.segment())},
			{func() ds.Key { return hashKey(pre.owned, c) }, ds.NewKey(pre.owned).Child(dshelp.MultihashToDsKey(c.Hash()))},
		}
		for i, tc := range cases {
			if got := tc.got(); got != tc.want {
				t.Fatalf("key %q case %d: %s, want %s", k, i, got, tc.want)
			}
			if a := testing.AllocsPerRun(100, func() { tc.got() }); a > 1 {
				t.Fatalf("key %q case %d: %.0f allocations", k, i, a)
			}
		}
	}
	if got, want := fmt.Sprint(appendCounter(nil, 7)), fmt.Sprint([]byte(fmt.Sprintf("%020d", 7))); got != want {
		t.Fatalf("counter %s, want %s", got, want)
	}
}
