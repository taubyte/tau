package pebbleds

import (
	"context"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/ipfs/boxo/blockstore"
	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	ipld "github.com/ipfs/go-ipld-format"
)

// Blockstore is a block store over s with boxo's layout: a block is kept
// under /blocks/<its multihash in unpadded base32>, so a store either one
// wrote reads in the other. It writes every block it is given, without
// asking first whether it has it.
type Blockstore struct{ s *Store }

var _ blockstore.Blockstore = (*Blockstore)(nil)

// Blockstore is the block store over s.
func (s *Store) Blockstore() *Blockstore { return &Blockstore{s: s} }

const blocksPrefix = "/blocks/"

// keyEncoding is the base32 of boxo's block keys: the standard alphabet,
// unpadded.
var keyEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

var keyPool = sync.Pool{New: func() any { return new([]byte) }}

// withKey calls fn with the key of c's block, built in a pooled buffer.
func withKey(c cid.Cid, fn func(k []byte) error) error {
	buf := keyPool.Get().(*[]byte)
	defer keyPool.Put(buf)
	*buf = keyEncoding.AppendEncode(append((*buf)[:0], blocksPrefix...), hashOf(c))
	return fn(*buf)
}

// hashOf is c's multihash, over c's own bytes.
func hashOf(c cid.Cid) []byte {
	s := c.KeyString()
	b := unsafe.Slice(unsafe.StringData(s), len(s))
	if c.Version() == 0 {
		return b
	}
	_, n := binary.Uvarint(b)
	_, m := binary.Uvarint(b[n:])
	return b[n+m:]
}

func notFound(c cid.Cid, err error) error {
	if errors.Is(err, ds.ErrNotFound) {
		return ipld.ErrNotFound{Cid: c}
	}
	return err
}

// Get returns c's block, its bytes copied.
func (b *Blockstore) Get(_ context.Context, c cid.Cid) (blocks.Block, error) {
	if !c.Defined() {
		return nil, ipld.ErrNotFound{Cid: c}
	}
	var data []byte
	err := withKey(c, func(k []byte) error {
		return b.s.View(k, func(v []byte) error {
			data = append(make([]byte, 0, len(v)), v...)
			return nil
		})
	})
	if err != nil {
		return nil, notFound(c, err)
	}
	return blocks.NewBlockWithCid(data, c)
}

// Has reports whether c's block is held.
func (b *Blockstore) Has(_ context.Context, c cid.Cid) (bool, error) {
	err := withKey(c, func(k []byte) error {
		return b.s.View(k, func([]byte) error { return nil })
	})
	if errors.Is(err, ds.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// GetSize is the length of c's block.
func (b *Blockstore) GetSize(_ context.Context, c cid.Cid) (int, error) {
	size := -1
	err := withKey(c, func(k []byte) error {
		return b.s.View(k, func(v []byte) error {
			size = len(v)
			return nil
		})
	})
	return size, notFound(c, err)
}

// Put stores blk.
func (b *Blockstore) Put(_ context.Context, blk blocks.Block) error {
	return withKey(blk.Cid(), func(k []byte) error {
		if !b.s.enter() {
			return ErrClosed
		}
		defer b.s.leave()
		if err := b.s.db.Set(k, blk.RawData(), b.s.write); err != nil {
			return fmt.Errorf("pebbleds: set: %w", err)
		}
		b.s.written.Add(int64(len(k) + len(blk.RawData())))
		return nil
	})
}

// PutMany stores blks in one batch.
func (b *Blockstore) PutMany(ctx context.Context, blks []blocks.Block) error {
	bt, err := b.s.NewBatch()
	if err != nil {
		return err
	}
	for _, blk := range blks {
		if err := withKey(blk.Cid(), func(k []byte) error { return bt.Set(k, blk.RawData()) }); err != nil {
			return err
		}
	}
	return bt.Commit(ctx)
}

// DeleteBlock removes c's block. Removing an absent one is not an error.
func (b *Blockstore) DeleteBlock(_ context.Context, c cid.Cid) error {
	return withKey(c, func(k []byte) error {
		if !b.s.enter() {
			return ErrClosed
		}
		defer b.s.leave()
		if err := b.s.db.Delete(k, b.s.write); err != nil {
			return fmt.Errorf("pebbleds: delete: %w", err)
		}
		return nil
	})
}

// AllKeysChan sends the cid of every block held, as a raw cid of its
// multihash: the store keeps no codec. It stops when ctx is done or the
// store closes.
func (b *Blockstore) AllKeysChan(ctx context.Context) (<-chan cid.Cid, error) {
	res, err := b.s.Query(ctx, query.Query{Prefix: blocksPrefix, KeysOnly: true})
	if err != nil {
		return nil, err
	}
	out := make(chan cid.Cid, query.KeysOnlyBufSize)
	go func() {
		defer close(out)
		defer res.Close()
		for {
			r, ok := res.NextSync()
			if !ok {
				return
			}
			if r.Error != nil {
				logger.Errorf("listing blocks: %v", r.Error)
				return
			}
			h, err := keyEncoding.DecodeString(r.Key[len(blocksPrefix):])
			if err != nil {
				logger.Warnf("a block key that is not base32: %q", r.Key)
				continue
			}
			select {
			case out <- cid.NewCidV1(cid.Raw, h):
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
