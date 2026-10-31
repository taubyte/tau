package mst

import (
	"reflect"
	"testing"

	cid "github.com/ipfs/go-cid"
	mh "github.com/multiformats/go-multihash"
)

// TestCidOverIsACid: a cid.Cid is one string of the cid's bytes, which
// cidOver casts to; go-cid changing that fails here, not in a read.
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
		got, ok := cidOver([]byte(want.KeyString()))
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
		if _, ok := cidOver(b); ok {
			t.Errorf("%s: taken for a cid", name)
		}
		if _, err := cid.Cast(b); err == nil {
			t.Errorf("%s: go-cid takes it", name)
		}
	}
}
