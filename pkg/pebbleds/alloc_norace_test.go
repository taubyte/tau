//go:build !race

package pebbleds

import (
	"context"
	"testing"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
)

// The native calls lend the store's bytes, so a warm call allocates nothing;
// the go-datastore calls allocate what they hand over.
func TestAllocs(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for _, k := range []string{"/p/a", "/p/b", "/p/c", "/q/a"} {
		must(t, s.Put(ctx, ds.RawKey(k), []byte("value")))
	}
	key, prefix := []byte("/p/b"), []byte("/p/")
	view := func(v []byte) error { return nil }
	scan := func(_, _ []byte) error { return nil }
	dk := ds.RawKey("/p/b")
	bs, blk := s.Blockstore(), block(t, "block", cid.Raw)
	cases := []struct {
		name string
		max  float64
		fn   func()
	}{
		{"View", 0, func() { must(t, s.View(key, view)) }},
		{"Scan", 0, func() { must(t, s.Scan(prefix, scan)) }},
		{"Range", 0, func() { must(t, s.Range(prefix, nil, scan)) }},
		{"Has", 0, func() {
			if _, err := s.Has(ctx, dk); err != nil {
				t.Fatal(err)
			}
		}},
		{"Get", 1, func() {
			if _, err := s.Get(ctx, dk); err != nil {
				t.Fatal(err)
			}
		}},
		{"Put", 0, func() { must(t, s.Put(ctx, dk, key)) }},
		{"Blockstore.Put", 0, func() { must(t, bs.Put(ctx, blk)) }},
		{"Blockstore.Has", 0, func() {
			if _, err := bs.Has(ctx, blk.Cid()); err != nil {
				t.Fatal(err)
			}
		}},
		{"Blockstore.Get", 2, func() {
			if _, err := bs.Get(ctx, blk.Cid()); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		c.fn()
		if got := testing.AllocsPerRun(200, c.fn); got > c.max {
			t.Errorf("%s: %.1f allocs, want at most %.0f", c.name, got, c.max)
		}
	}
}
