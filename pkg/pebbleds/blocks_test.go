package pebbleds

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cockroachdb/pebble/v2"
	"github.com/ipfs/boxo/blockstore"
	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	gopebble "github.com/ipfs/go-ds-pebble"
	ipld "github.com/ipfs/go-ipld-format"
	mh "github.com/multiformats/go-multihash"
)

func block(t testing.TB, data string, codec uint64) blocks.Block {
	t.Helper()
	c, err := cid.Prefix{Version: 1, Codec: codec, MhType: mh.SHA2_256, MhLength: -1}.Sum([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	b, err := blocks.NewBlockWithCid([]byte(data), c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBlockstore(t *testing.T) {
	ctx := context.Background()
	bs := open(t).Blockstore()
	a, b, c := block(t, "a", cid.Raw), block(t, "b", cid.DagProtobuf), block(t, "c", cid.Raw)
	must(t, bs.Put(ctx, a))
	must(t, bs.PutMany(ctx, []blocks.Block{b, c}))
	for _, blk := range []blocks.Block{a, b, c} {
		got, err := bs.Get(ctx, blk.Cid())
		must(t, err)
		if string(got.RawData()) != string(blk.RawData()) || !got.Cid().Equals(blk.Cid()) {
			t.Fatalf("%s: %q", blk.Cid(), got.RawData())
		}
		if has, err := bs.Has(ctx, blk.Cid()); err != nil || !has {
			t.Fatalf("has %s: %v %v", blk.Cid(), has, err)
		}
		if n, err := bs.GetSize(ctx, blk.Cid()); err != nil || n != len(blk.RawData()) {
			t.Fatalf("size %s: %d %v", blk.Cid(), n, err)
		}
	}
	// A block is kept by its multihash: the same bytes under another codec
	// are the same block.
	other := block(t, "a", cid.DagProtobuf)
	if has, err := bs.Has(ctx, other.Cid()); err != nil || !has {
		t.Fatalf("a block is its multihash: %v %v", has, err)
	}
	must(t, bs.DeleteBlock(ctx, a.Cid()))
	must(t, bs.DeleteBlock(ctx, a.Cid()))
	if _, err := bs.Get(ctx, a.Cid()); !ipld.IsNotFound(err) {
		t.Fatalf("get of a removed block: %v", err)
	}
	if _, err := bs.GetSize(ctx, a.Cid()); !ipld.IsNotFound(err) {
		t.Fatalf("size of a removed block: %v", err)
	}
	if has, err := bs.Has(ctx, a.Cid()); err != nil || has {
		t.Fatalf("has a removed block: %v %v", has, err)
	}
	if _, err := bs.Get(ctx, cid.Undef); !ipld.IsNotFound(err) {
		t.Fatalf("get of no cid: %v", err)
	}
	ch, err := bs.AllKeysChan(ctx)
	must(t, err)
	got := map[string]bool{}
	for k := range ch {
		got[string(k.Hash())] = true
	}
	if len(got) != 2 || !got[string(b.Cid().Hash())] || !got[string(c.Cid().Hash())] {
		t.Fatalf("all keys: %d", len(got))
	}
}

func TestBlockstoreAllKeysStops(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	must(t, err)
	bs := s.Blockstore()
	for _, d := range []string{"a", "b", "c", "d"} {
		must(t, bs.Put(ctx, block(t, d, cid.Raw)))
	}
	cctx, cancel := context.WithCancel(ctx)
	ch, err := bs.AllKeysChan(cctx)
	must(t, err)
	<-ch
	cancel()
	for range ch {
	}
	ch, err = bs.AllKeysChan(ctx)
	must(t, err)
	must(t, s.Close())
	for range ch {
	}
	if _, err := bs.AllKeysChan(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("listing a closed store: %v", err)
	}
	blk := block(t, "e", cid.Raw)
	for i, err := range []error{bs.Put(ctx, blk), bs.PutMany(ctx, []blocks.Block{blk}), bs.DeleteBlock(ctx, blk.Cid())} {
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("write %d after Close: %v", i, err)
		}
	}
	if _, err := bs.Has(ctx, blk.Cid()); !errors.Is(err, ErrClosed) {
		t.Fatalf("has after Close: %v", err)
	}
	if _, err := bs.Get(ctx, blk.Cid()); !errors.Is(err, ErrClosed) {
		t.Fatalf("get after Close: %v", err)
	}
}

func TestBlockstoreSameLayoutAsBoxo(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, b := block(t, "theirs", cid.DagProtobuf), block(t, "ours", cid.Raw)
	theirs, err := gopebble.NewDatastore(dir)
	must(t, err)
	must(t, blockstore.NewBlockstore(theirs).Put(ctx, a))
	must(t, theirs.Close())
	ours, err := Open(dir)
	must(t, err)
	got, err := ours.Blockstore().Get(ctx, a.Cid())
	must(t, err)
	if string(got.RawData()) != "theirs" {
		t.Fatalf("%q", got.RawData())
	}
	must(t, ours.Blockstore().Put(ctx, b))
	must(t, ours.Close())
	theirs, err = gopebble.NewDatastore(dir)
	must(t, err)
	defer func() { must(t, theirs.Close()) }()
	got, err = blockstore.NewBlockstore(theirs).Get(ctx, b.Cid())
	must(t, err)
	if string(got.RawData()) != "ours" {
		t.Fatalf("%q", got.RawData())
	}
}

func TestBlockstoreCidV0(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	bs := s.Blockstore()
	h, err := mh.Sum([]byte("v0"), mh.SHA2_256, -1)
	must(t, err)
	v0 := cid.NewCidV0(h)
	blk, err := blocks.NewBlockWithCid([]byte("v0"), v0)
	must(t, err)
	must(t, bs.Put(ctx, blk))
	// A v0 cid is its multihash: its block is the v1 cid's of that hash.
	if has, err := bs.Has(ctx, cid.NewCidV1(cid.DagProtobuf, h)); err != nil || !has {
		t.Fatalf("v0 and v1 name one block: %v %v", has, err)
	}
	must(t, s.Put(ctx, ds.RawKey("/blocks/not-base32!"), nil))
	ch, err := bs.AllKeysChan(ctx)
	must(t, err)
	n := 0
	for range ch {
		n++
	}
	if n != 1 {
		t.Fatalf("a key that is no block's is skipped: %d", n)
	}
}

// BenchmarkBlockstoreHas: a block store's Has, the call bitswap makes for
// every block a peer wants, of a block held and of one not, over 100k
// blocks in tables: boxo's ARC and bloom cache over a store with no
// filters, as a node had it, against this store's own bloom filters.
func BenchmarkBlockstoreHas(b *testing.B) {
	ctx := context.Background()
	const n = 100_000
	stores := map[string]func(b *testing.B) blockstore.Blockstore{
		"boxo-cache": func(b *testing.B) blockstore.Blockstore {
			s, err := Open(b.TempDir(), WithPebbleOptions(&pebble.Options{Levels: [7]pebble.LevelOptions{{FilterPolicy: pebble.NoFilterPolicy}}}))
			must(b, err)
			b.Cleanup(func() { must(b, s.Close()) })
			fill(b, s, n)
			cctx, cancel := context.WithCancel(ctx)
			b.Cleanup(cancel)
			bs, err := blockstore.CachedBlockstore(cctx, blockstore.NewIdStore(s.Blockstore()), blockstore.DefaultCacheOpts())
			must(b, err)
			must(b, bs.(interface{ Wait(context.Context) error }).Wait(ctx))
			return bs
		},
		"pebble-bloom": func(b *testing.B) blockstore.Blockstore {
			s := open(b)
			fill(b, s, n)
			return blockstore.NewIdStore(s.Blockstore())
		},
	}
	for _, name := range []string{"boxo-cache", "pebble-bloom"} {
		b.Run(name, func(b *testing.B) {
			bs := stores[name](b)
			held := make([]cid.Cid, 1024)
			absent := make([]cid.Cid, 1024)
			for i := range held {
				held[i] = block(b, fmt.Sprint(i*97), cid.Raw).Cid()
				absent[i] = block(b, fmt.Sprint("absent", i), cid.Raw).Cid()
			}
			for _, c := range []struct {
				name string
				cids []cid.Cid
				want bool
			}{{"hit", held, true}, {"miss", absent, false}} {
				b.Run(c.name, func(b *testing.B) {
					b.ReportAllocs()
					for i := range b.N {
						has, err := bs.Has(ctx, c.cids[i%len(c.cids)])
						if err != nil || has != c.want {
							b.Fatalf("has %v %v", has, err)
						}
					}
				})
			}
		})
	}
}

func fill(b *testing.B, s *Store, n int) {
	b.Helper()
	ctx := context.Background()
	bs := s.Blockstore()
	batch := make([]blocks.Block, 0, 1000)
	for i := range n {
		batch = append(batch, block(b, fmt.Sprint(i), cid.Raw))
		if len(batch) == cap(batch) {
			must(b, bs.PutMany(ctx, batch))
			batch = batch[:0]
		}
	}
	must(b, bs.PutMany(ctx, batch))
	must(b, s.db.Flush())
}
