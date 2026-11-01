package pebbleds

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
)

func ready(t testing.TB, s *Store) {
	t.Helper()
	for range 5000 {
		if s.blocks.ready.Load() && s.blocks.next.Load() == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the filter of blocks held was never built")
}

func blockKey(c cid.Cid) ds.Key {
	return ds.RawKey(blocksPrefix + keyEncoding.EncodeToString(hashOf(c)))
}

func TestBlockMemory(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir)
	must(t, err)
	old := block(t, "held before", cid.Raw)
	must(t, s.Blockstore().Put(ctx, old))
	must(t, s.Close())

	s, err = Open(dir)
	must(t, err)
	defer func() { must(t, s.Close()) }()
	bs := s.Blockstore()
	ready(t, s)
	has := func(c cid.Cid, want bool) {
		t.Helper()
		if got, err := bs.Has(ctx, c); err != nil || got != want {
			t.Fatalf("has %s: %v %v, want %v", c, got, err, want)
		}
	}
	has(old.Cid(), true)
	if !s.blocks.hits.has(hashOf(old.Cid())) {
		t.Fatal("a block found is not remembered")
	}
	has(block(t, "never", cid.Raw).Cid(), false)
	if _, err := bs.Get(ctx, block(t, "never", cid.Raw).Cid()); err == nil {
		t.Fatal("got a block never held")
	}
	if _, err := bs.GetSize(ctx, block(t, "never", cid.Raw).Cid()); err == nil {
		t.Fatal("sized a block never held")
	}

	viaDs := block(t, "through go-datastore", cid.Raw)
	must(t, s.Put(ctx, blockKey(viaDs.Cid()), viaDs.RawData()))
	has(viaDs.Cid(), true)
	must(t, s.Delete(ctx, blockKey(viaDs.Cid())))
	has(viaDs.Cid(), false)

	viaBatch := block(t, "through a batch", cid.Raw)
	b, err := s.NewBatch()
	must(t, err)
	must(t, b.Put(ctx, blockKey(viaBatch.Cid()), viaBatch.RawData()))
	must(t, b.Commit(ctx))
	has(viaBatch.Cid(), true)
	must(t, b.Reset())
	must(t, b.Delete(ctx, blockKey(viaBatch.Cid())))
	must(t, b.Commit(ctx))
	has(viaBatch.Cid(), false)

	must(t, bs.DeleteBlock(ctx, old.Cid()))
	has(old.Cid(), false)
}

func TestBlockFilterRebuilds(t *testing.T) {
	defer func(b uint64) { bloomBits = b }(bloomBits)
	bloomBits = 1 << 10
	ctx := context.Background()
	s := open(t)
	bs := s.Blockstore()
	ready(t, s)
	var all []blocks.Block
	for i := range 600 {
		b := block(t, fmt.Sprint("rebuild", i), cid.Raw)
		all = append(all, b)
		must(t, bs.Put(ctx, b))
	}
	ready(t, s)
	if bits := s.blocks.bloom.Load().mask + 1; bits <= 1<<10 {
		t.Fatalf("the filter is still %d bits", bits)
	}
	s.blocks.hits.clear()
	for _, b := range all {
		if !s.blocks.bloom.Load().mayHave(hashOf(b.Cid())) {
			t.Fatalf("the rebuilt filter lost %s", b.Cid())
		}
	}
}

func TestBlockMemoryRaces(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	bs := s.Blockstore()
	var held []blocks.Block
	for i := range 64 {
		held = append(held, block(t, fmt.Sprint("race", i), cid.Raw))
	}
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Go(func() {
			for i := range 300 {
				b := held[(i*7+w)%len(held)]
				switch i % 3 {
				case 0:
					must(t, bs.Put(ctx, b))
				case 1:
					must(t, bs.DeleteBlock(ctx, b.Cid()))
				default:
					if _, err := bs.Has(ctx, b.Cid()); err != nil {
						t.Error(err)
					}
				}
			}
		})
	}
	wg.Wait()
	for _, b := range held {
		got, err := bs.Has(ctx, b.Cid())
		must(t, err)
		var stored bool
		err = s.View([]byte(blockKey(b.Cid()).String()), func([]byte) error { stored = true; return nil })
		if err != nil && err != ds.ErrNotFound {
			t.Fatal(err)
		}
		if got != stored {
			t.Fatalf("%s: has %v, stored %v", b.Cid(), got, stored)
		}
	}
}

func TestBlockFilterStopsOnClose(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir)
	must(t, err)
	var batch []blocks.Block
	for i := range 20000 {
		batch = append(batch, block(t, fmt.Sprint("many", i), cid.Raw))
	}
	must(t, s.Blockstore().PutMany(ctx, batch))
	must(t, s.Close())
	s, err = Open(dir)
	must(t, err)
	s.Blockstore()
	must(t, s.Close())
}

func TestDoneContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := open(t)
	bs := s.Blockstore()
	blk := block(t, "x", cid.Raw)
	k := ds.RawKey("/k")
	b, err := s.NewBatch()
	must(t, err)
	_, getErr := s.Get(ctx, k)
	_, hasErr := s.Has(ctx, k)
	_, sizeErr := s.GetSize(ctx, k)
	_, queryErr := s.Query(ctx, query.Query{})
	_, bGetErr := bs.Get(ctx, blk.Cid())
	_, bHasErr := bs.Has(ctx, blk.Cid())
	_, bSizeErr := bs.GetSize(ctx, blk.Cid())
	for i, err := range []error{
		getErr, hasErr, sizeErr, queryErr, bGetErr, bHasErr, bSizeErr,
		s.Put(ctx, k, nil), s.Delete(ctx, k), s.Sync(ctx, k),
		b.Put(ctx, k, nil), b.Delete(ctx, k), b.Commit(ctx),
		bs.Put(ctx, blk), bs.PutMany(ctx, []blocks.Block{blk}), bs.DeleteBlock(ctx, blk.Cid()),
	} {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("call %d with a done context: %v", i, err)
		}
	}
	if has, err := bs.Has(context.Background(), blk.Cid()); err != nil || has {
		t.Fatalf("a put with a done context wrote: %v %v", has, err)
	}
}
