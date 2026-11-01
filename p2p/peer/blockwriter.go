package peer

import (
	"context"
	"sync"

	"github.com/ipfs/boxo/blockstore"
	exchange "github.com/ipfs/boxo/exchange"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
)

const notifyBatch = 256

// A BlockWriter stores a block before WriteBlock returns and tells the
// exchange from one goroutine, batching every block written meanwhile. Telling
// late only delays a peer that asked before the write: a want is answered from
// the store.
type BlockWriter struct {
	bs      blockstore.Blockstore
	exch    exchange.Interface
	queue   chan blocks.Block
	ctx     context.Context
	cancel  context.CancelFunc
	stopped chan struct{}
	once    sync.Once
}

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

// WriteBlock does not ask first whether b is held. With notifyBatch blocks
// waiting it tells the exchange itself.
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

// Removing an absent block is not an error.
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

// A block still waiting is stored, but the exchange does not hear of it.
func (w *BlockWriter) Close() {
	w.once.Do(w.cancel)
	<-w.stopped
}
