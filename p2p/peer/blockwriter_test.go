package peer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ipfs/boxo/blockstore"
	exchange "github.com/ipfs/boxo/exchange"
	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
)

// heard records what an exchange is told; release, when set, holds each
// call until it is closed.
type heard struct {
	exchange.Interface
	mu      sync.Mutex
	calls   [][]cid.Cid
	release chan struct{}
	entered chan struct{}
	err     error
}

func (h *heard) NotifyNewBlocks(_ context.Context, bs ...blocks.Block) error {
	if h.entered != nil {
		h.entered <- struct{}{}
	}
	if h.release != nil {
		<-h.release
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var cs []cid.Cid
	for _, b := range bs {
		cs = append(cs, b.Cid())
	}
	h.calls = append(h.calls, cs)
	return h.err
}

func (h *heard) told() (calls, blocks int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.calls {
		blocks += len(c)
	}
	return len(h.calls), blocks
}

func newBlockstore() blockstore.Blockstore {
	return blockstore.NewBlockstore(dssync.MutexWrap(datastore.NewMapDatastore()))
}

func eventually(t *testing.T, f func() bool) {
	t.Helper()
	for range 2000 {
		if f() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("never")
}

func TestBlockWriterStoresThenTells(t *testing.T) {
	ctx := context.Background()
	bs, h := newBlockstore(), &heard{}
	w := NewBlockWriter(bs, h)
	defer w.Close()
	b := blocks.NewBlock([]byte("one"))
	if err := w.WriteBlock(ctx, b); err != nil {
		t.Fatal(err)
	}
	if has, err := bs.Has(ctx, b.Cid()); err != nil || !has {
		t.Fatalf("a block is stored before WriteBlock returns: %v %v", has, err)
	}
	eventually(t, func() bool { _, n := h.told(); return n == 1 })
}

// While the exchange hears of one block, every block written meanwhile
// waits and is told of in one call.
func TestBlockWriterTellsInBatches(t *testing.T) {
	ctx := context.Background()
	h := &heard{release: make(chan struct{}), entered: make(chan struct{}, notifyBatch*2)}
	w := NewBlockWriter(newBlockstore(), h)
	defer w.Close()
	if err := w.WriteBlock(ctx, blocks.NewBlock([]byte("first"))); err != nil {
		t.Fatal(err)
	}
	<-h.entered
	for i := range 10 {
		if err := w.WriteBlock(ctx, blocks.NewBlock([]byte{byte(i)})); err != nil {
			t.Fatal(err)
		}
	}
	close(h.release)
	eventually(t, func() bool { _, n := h.told(); return n == 11 })
	if calls, _ := h.told(); calls != 2 {
		t.Fatalf("11 blocks told in %d calls, want 2", calls)
	}
}

// With notifyBatch blocks waiting, a writer tells the exchange itself
// rather than wait.
func TestBlockWriterTellsItselfWhenFull(t *testing.T) {
	ctx := context.Background()
	h := &heard{release: make(chan struct{}), entered: make(chan struct{}, notifyBatch*2), err: errors.New("told")}
	w := NewBlockWriter(newBlockstore(), h)
	if err := w.WriteBlock(ctx, blocks.NewBlock([]byte("first"))); err != nil {
		t.Fatal(err)
	}
	<-h.entered
	for i := range notifyBatch {
		if err := w.WriteBlock(ctx, blocks.NewBlock([]byte{byte(i), byte(i >> 8)})); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- w.WriteBlock(ctx, blocks.NewBlock([]byte("overflow"))) }()
	<-h.entered
	close(h.release)
	if err := <-done; err != nil {
		t.Fatalf("the exchange failing is not the write failing: %v", err)
	}
	eventually(t, func() bool { _, n := h.told(); return n == notifyBatch+2 })
	w.Close()
	w.Close()
}

type failingStore struct{ blockstore.Blockstore }

func (failingStore) Put(context.Context, blocks.Block) error { return errors.New("full") }

func TestBlockWriterStoreFails(t *testing.T) {
	h := &heard{}
	w := NewBlockWriter(failingStore{newBlockstore()}, h)
	defer w.Close()
	if err := w.WriteBlock(context.Background(), blocks.NewBlock([]byte("x"))); err == nil {
		t.Fatal("a block the store refused was written")
	}
	time.Sleep(10 * time.Millisecond)
	if _, n := h.told(); n != 0 {
		t.Fatal("the exchange heard of a block not stored")
	}
}

func TestBlockWriterDeletes(t *testing.T) {
	ctx := context.Background()
	bs := newBlockstore()
	w := NewBlockWriter(bs, &heard{})
	defer w.Close()
	a, b := blocks.NewBlock([]byte("a")), blocks.NewBlock([]byte("b"))
	for _, blk := range []blocks.Block{a, b} {
		if err := w.WriteBlock(ctx, blk); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.DeleteBlocks(ctx, []cid.Cid{a.Cid(), b.Cid(), blocks.NewBlock([]byte("never")).Cid()}); err != nil {
		t.Fatal(err)
	}
	for _, blk := range []blocks.Block{a, b} {
		if has, err := bs.Has(ctx, blk.Cid()); err != nil || has {
			t.Fatalf("a deleted block is held: %v %v", has, err)
		}
	}
	refusing := NewBlockWriter(failingDeletes{bs}, &heard{})
	defer refusing.Close()
	if err := refusing.DeleteBlocks(ctx, []cid.Cid{a.Cid()}); err == nil {
		t.Fatal("a delete the store refused succeeded")
	}
}

type failingDeletes struct{ blockstore.Blockstore }

func (failingDeletes) DeleteBlock(context.Context, cid.Cid) error { return errors.New("refused") }
