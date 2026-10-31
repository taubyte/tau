package peer

import (
	"context"
	"sync"

	"github.com/ipfs/boxo/blockstore"
	exchange "github.com/ipfs/boxo/exchange"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
)

// notifyBatch bounds the blocks one call tells the exchange of, and the
// blocks waiting for one.
const notifyBatch = 256

// A BlockWriter stores blocks and tells an exchange it holds them, in
// batches. A block is stored before WriteBlock returns; the exchange hears
// of it from one goroutine, with every block written while it was hearing
// of the ones before, so a burst of writes is one call. Hearing late only
// delays a peer that asked for the block before it was written: a want is
// answered from the block store.
type BlockWriter struct {
	bs      blockstore.Blockstore
	exch    exchange.Interface
	queue   chan blocks.Block
	ctx     context.Context
	cancel  context.CancelFunc
	stopped chan struct{}
	once    sync.Once
}

// NewBlockWriter is a writer to bs telling exch.
func NewBlockWriter(bs blockstore.Blockstore, exch exchange.Interface) *BlockWriter {
	ctx, cancel := context.WithCancel(context.Background())
	w := &BlockWriter{
		bs:      bs,
		exch:    exch,
		queue:   make(chan blocks.Block, notifyBatch),
		ctx:     ctx,
		cancel:  cancel,
		stopped: make(chan struct{}),
	}
	go w.notify()
	return w
}

// WriteBlock stores b, without asking first whether it is held, and has the
// exchange told. With notifyBatch blocks already waiting, it tells the
// exchange itself.
func (w *BlockWriter) WriteBlock(ctx context.Context, b blocks.Block) error {
	if err := w.bs.Put(ctx, b); err != nil {
		return err
	}
	select {
	case w.queue <- b:
	default:
		if err := w.exch.NotifyNewBlocks(ctx, b); err != nil {
			logger.Errorf("telling the exchange of %s: %v", b.Cid(), err)
		}
	}
	return nil
}

// DeleteBlocks removes cids' blocks from the store, as the block service's
// DeleteBlock does, without its context and span per block. Removing an
// absent block is not an error.
func (w *BlockWriter) DeleteBlocks(ctx context.Context, cids []cid.Cid) error {
	for _, c := range cids {
		if err := w.bs.DeleteBlock(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

func (w *BlockWriter) notify() {
	defer close(w.stopped)
	batch := make([]blocks.Block, 0, notifyBatch)
	for {
		select {
		case b := <-w.queue:
			batch = append(batch[:0], b)
		more:
			for len(batch) < cap(batch) {
				select {
				case b := <-w.queue:
					batch = append(batch, b)
				default:
					break more
				}
			}
			if err := w.exch.NotifyNewBlocks(w.ctx, batch...); err != nil {
				logger.Errorf("telling the exchange of %d blocks: %v", len(batch), err)
			}
			clear(batch)
		case <-w.ctx.Done():
			return
		}
	}
}

// Close stops telling the exchange and returns once it no longer is. A
// block still waiting is stored; the exchange does not hear of it.
func (w *BlockWriter) Close() {
	w.once.Do(w.cancel)
	<-w.stopped
}
