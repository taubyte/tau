package statetree

import (
	"bytes"
	"reflect"
	"slices"
	"testing"

	cid "github.com/ipfs/go-cid"
	mh "github.com/multiformats/go-multihash"
)

// CidOver casts bytes to a cid.Cid: this fails if go-cid stops holding a cid
// as one string of its bytes.
func TestCidOverIsACid(t *testing.T) {
	typ := reflect.TypeFor[cid.Cid]()
	if typ.NumField() != 1 || typ.Field(0).Type.Kind() != reflect.String {
		t.Fatalf("cid.Cid is no longer one string: %v", typ)
	}
	h, err := mh.Sum([]byte("x"), mh.SHA2_256, -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []cid.Cid{cid.NewCidV0(h), cid.NewCidV1(cid.Raw, h), cid.NewCidV1(cid.DagCBOR, h)} {
		got, ok := CidOver([]byte(want.KeyString()))
		if !ok || got != want || got.String() != want.String() || !got.Equals(want) {
			t.Fatalf("%s came back as %v (%v)", want, got, ok)
		}
	}
	for name, b := range map[string][]byte{
		"empty":              nil,
		"version 2":          append([]byte{2}, []byte(cid.NewCidV1(cid.Raw, h).KeyString())[1:]...),
		"short digest":       []byte(cid.NewCidV1(cid.Raw, h).KeyString())[:20],
		"trailing byte":      append([]byte(cid.NewCidV1(cid.Raw, h).KeyString()), 0),
		"non-minimal varint": append([]byte{0x81, 0x00}, []byte(cid.NewCidV1(cid.Raw, h).KeyString())[1:]...),
	} {
		if _, ok := CidOver(b); ok {
			t.Errorf("%s: taken for a cid", name)
		}
		if _, err := cid.Cast(b); err == nil {
			t.Errorf("%s: go-cid takes it", name)
		}
	}
}

func TestABlockCidSharesItsBytes(t *testing.T) {
	h, err := mh.Sum([]byte("x"), mh.SHA2_256, -1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewBlock([]byte("data"), []cid.Cid{cid.NewCidV1(cid.Raw, h)})
	if err != nil {
		t.Fatal(err)
	}
	sum, err := mh.Sum(b.RawData(), mh.SHA2_256, -1)
	if err != nil {
		t.Fatal(err)
	}
	want := cid.NewCidV1(cid.Raw, sum)
	if b.Cid() != want || !IsBlockCid(b.Cid()) {
		t.Fatalf("cid %s, want %s", b.Cid(), want)
	}
	if grown := append(b.RawData(), make([]byte, 64)...); &grown[0] == &b.RawData()[0] || b.Cid() != want {
		t.Fatalf("an append to the block reached its cid: %s", b.Cid())
	}
}

func TestABlockInItsBufferIsTheBlockOfItsBytes(t *testing.T) {
	want, err := NewBlock([]byte("data"), nil)
	if err != nil {
		t.Fatal(err)
	}
	room := BlockBuffer(len(want.RawData()))
	copy(room, want.RawData())
	for _, raw := range [][]byte{room, slices.Clip(bytes.Clone(want.RawData()))} {
		b, err := BlockInBuffer(raw)
		if err != nil || b.Cid() != want.Cid() || !bytes.Equal(b.RawData(), want.RawData()) || cap(b.RawData()) != len(b.RawData()) {
			t.Fatalf("block %v (%v), want %s", b, err, want.Cid())
		}
	}
}
