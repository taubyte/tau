package kvdb

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"testing"

	dshelp "github.com/ipfs/boxo/datastore/dshelp"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	mh "github.com/multiformats/go-multihash"
)

func TestKeysAreTheirSegments(t *testing.T) {
	ns := ds.NewKey("/ns")
	s := newSet(nil, ns.ChildString(setNs), new(internTable))
	pre := newPrefixes(ns)
	hh, err := newHeads(context.Background(), ds.NewMapDatastore(), ns.ChildString(headsNs), new(internTable))
	if err != nil {
		t.Fatal(err)
	}
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
		type testCase struct {
			got  func() ds.Key
			want ds.Key
		}
		cases := []testCase{
			{func() ds.Key { return s.keyElemsPrefix(k) }, ds.NewKey(s.elems).ChildString(keySegment(k))},
			{func() ds.Key { return s.elemKey(k, d) }, ds.NewKey(s.elems).ChildString(keySegment(k)).ChildString(d.segment())},
			{func() ds.Key { return s.pendingKey(k, d) }, ds.NewKey(s.pending).ChildString(d.segment()).ChildString(keySegment(k))},
			{func() ds.Key { return dotKey(pre.missing, d) }, ds.NewKey(pre.missing).ChildString(d.segment())},
			{func() ds.Key { return dotKey(pre.retry, d) }, ns.ChildString(retryNs).ChildString(d.segment())},
			{func() ds.Key { return hashKey(pre.reclaim, c) }, ds.NewKey(pre.reclaim).Child(dshelp.MultihashToDsKey(c.Hash()))},
			{func() ds.Key { return hh.key(c) }, ns.ChildString(headsNs).Child(dshelp.MultihashToDsKey(c.Hash()))},
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

func TestParsedSegmentsAreInterned(t *testing.T) {
	var replicas internTable
	d := dot{replica: "replica\x00\xff-interned", counter: 42}
	seg := d.segment()
	got, err := replicas.parseDotSegment(seg)
	if err != nil || got != d {
		t.Fatalf("parsed %v, %v", got, err)
	}
	if a := testing.AllocsPerRun(100, func() {
		if _, err := replicas.parseDotSegment(seg); err != nil {
			t.Fatal(err)
		}
	}); a != 0 {
		t.Fatalf("parsing a dot segment: %.0f allocations", a)
	}
	p, v := d.toPB(), encodeDot(d)
	if replicas.dotFromPB(p) != d {
		t.Fatal("dot from its message")
	}
	if got, err := replicas.decodeDot(v); err != nil || got != d {
		t.Fatalf("dot from its value: %v, %v", got, err)
	}
	if a := testing.AllocsPerRun(100, func() {
		replicas.dotFromPB(p)
		if _, err := replicas.decodeDot(v); err != nil {
			t.Fatal(err)
		}
	}); a != 0 {
		t.Fatalf("reading a dot: %.0f allocations", a)
	}

	long := string(make([]byte, 200))
	for _, k := range []string{"", "/a", "x\x00y", long} {
		ks := keySegment(k)
		if got, err := parseKeySegment(ks); err != nil || got != k {
			t.Fatalf("key %q parsed to %q, %v", k, got, err)
		}
		if a := testing.AllocsPerRun(100, func() {
			if _, err := parseKeySegment(ks); err != nil {
				t.Fatal(err)
			}
		}); a > 1 {
			t.Fatalf("parsing the segment of %q: %.0f allocations", k, a)
		}
	}
	if _, err := parseKeySegment("k!!"); err == nil {
		t.Fatal("a bad key segment parsed")
	}
	if _, err := replicas.parseDotSegment("r!!." + fmt.Sprintf("%020d", 1)); err == nil {
		t.Fatal("a bad dot segment parsed")
	}
}

func TestInternTableForgetsAtItsLimit(t *testing.T) {
	tab := &internTable{m: map[string]string{}, raw: map[string]string{}}
	for i := range internedLimit + 1 {
		r := fmt.Sprintf("replica-%d", i)
		if got, err := tab.of(segEncoding.EncodeToString([]byte(r))); err != nil || got != r {
			t.Fatalf("%q: %q, %v", r, got, err)
		}
		if got := tab.bytes([]byte(r)); got != r {
			t.Fatalf("%q: %q", r, got)
		}
	}
	if len(tab.m) > internedLimit || len(tab.raw) > internedLimit {
		t.Fatalf("the table holds %d and %d replicas", len(tab.m), len(tab.raw))
	}
}

func TestABlockRecordIsOneAllocation(t *testing.T) {
	h, err := mh.Sum([]byte("state"), mh.SHA2_256, -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []block{
		{kind: kindDelta, dot: dot{replica: "r\x00", counter: 300}, at: 1 << 40, ttl: 7},
		{kind: kindDelta, dot: dot{replica: "rr", counter: 1}, at: 5, ttl: 1 << 20, state: cid.NewCidV1(cid.DagProtobuf, h)},
	} {
		want := append([]byte{byte(b.kind)}, encodeStamps(b.at, b.ttl)...)
		want = append(binary.AppendUvarint(want, uint64(len(b.state.Bytes()))), b.state.Bytes()...)
		want = append(want, encodeDot(b.dot)...)
		got := encodeBlock(b)
		if !bytes.Equal(got, want) {
			t.Fatalf("record %x, want %x", got, want)
		}
		back, err := new(internTable).decodeBlock(got)
		if err != nil || back != b {
			t.Fatalf("read back %+v, %v", back, err)
		}
		if a := testing.AllocsPerRun(100, func() { encodeBlock(b) }); a != 1 {
			t.Fatalf("encoding a block record: %.0f allocations", a)
		}
	}
}

func TestABlockKeyNamesItsCidInOneAllocation(t *testing.T) {
	prefix := ds.NewKey("/ns/b")
	h, err := mh.Sum([]byte("block"), mh.SHA2_256, -1)
	if err != nil {
		t.Fatal(err)
	}
	c := cid.NewCidV1(cid.Raw, h)
	k := hashKey(prefix.String(), c).String()
	got, err := cidOfBlockKey(prefix, k)
	if err != nil || got != c {
		t.Fatalf("cid of %s: %s, %v; want %s", k, got, err, c)
	}
	if a := testing.AllocsPerRun(100, func() { cidOfBlockKey(prefix, k) }); a != 1 {
		t.Fatalf("reading a block key: %.0f allocations", a)
	}
	for _, bad := range []string{prefix.String(), prefix.String() + "/!!", prefix.String() + "/AE"} {
		if _, err := cidOfBlockKey(prefix, bad); err == nil {
			t.Fatalf("cid of %q: no error", bad)
		}
	}
}

func TestASegmentDecodesAsItsEncoding(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for n := range 40 {
		src := make([]byte, n)
		for i := range src {
			src[i] = byte(rng.Uint32())
		}
		enc := segEncoding.EncodeToString(src)
		got, err := appendSegDecode([]byte("x"), []byte(enc))
		if err != nil || !bytes.Equal(got, append([]byte("x"), src...)) {
			t.Fatalf("%q decoded to %x, %v; want %x", enc, got, err, src)
		}
	}
	for _, bad := range []string{"A", "AAA", "AAAAAA", "a", "AE=", "A1", "AE/"} {
		if _, err := appendSegDecode(nil, []byte(bad)); err == nil {
			t.Fatalf("%q decoded", bad)
		}
	}
}
