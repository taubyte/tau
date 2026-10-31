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

// Blockstore is the block store over s. The first starts building the
// filter of blocks held.
func (s *Store) Blockstore() *Blockstore {
	s.blocks.start(s)
	return &Blockstore{s: s}
}

func isBlockKey(k []byte) bool {
	return len(k) > len(blocksPrefix) && string(k[:len(blocksPrefix)]) == blocksPrefix
}

// usable is the error a call to b gets before reading: ctx's, or ErrClosed.
func (b *Blockstore) usable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.s.closed.Load() {
		return ErrClosed
	}
	return nil
}

// noteBlockSet puts k's block, when k is a block's key and written, in the
// filter: a store's go-datastore writes are block writes too.
func (s *Store) noteBlockSet(k []byte) {
	if !isBlockKey(k) || s.blocks.bloom.Load() == nil {
		return
	}
	var h [128]byte
	if n, err := keyEncoding.Decode(h[:], k[len(blocksPrefix):]); err == nil {
		s.blocks.filter(s, h[:n])
	}
}

// deleted forgets every block recently held once a block was deleted
// through the go-datastore calls, which name no cid.
func (s *Store) deleted() {
	s.blocks.deletes.Add(1)
	s.blocks.hits.clear()
}

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
func (b *Blockstore) Get(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	if err := b.usable(ctx); err != nil {
		return nil, err
	}
	if !c.Defined() || !b.s.blocks.mayHave(hashOf(c)) {
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

// Has reports whether c's block is held, from memory when it can.
func (b *Blockstore) Has(ctx context.Context, c cid.Cid) (bool, error) {
	if err := b.usable(ctx); err != nil {
		return false, err
	}
	h := hashOf(c)
	if b.s.blocks.hits.has(h) {
		return true, nil
	}
	if !b.s.blocks.mayHave(h) {
		return false, nil
	}
	epoch := b.s.blocks.deletes.Load()
	err := withKey(c, func(k []byte) error {
		return b.s.View(k, func([]byte) error { return nil })
	})
	if errors.Is(err, ds.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	b.s.blocks.hits.add(h)
	if b.s.blocks.deletes.Load() != epoch {
		b.s.blocks.hits.drop(h)
	}
	return true, nil
}

// GetSize is the length of c's block.
func (b *Blockstore) GetSize(ctx context.Context, c cid.Cid) (int, error) {
	if err := b.usable(ctx); err != nil {
		return -1, err
	}
	if !b.s.blocks.mayHave(hashOf(c)) {
		return -1, ipld.ErrNotFound{Cid: c}
	}
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
func (b *Blockstore) Put(ctx context.Context, blk blocks.Block) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return withKey(blk.Cid(), func(k []byte) error {
		if !b.s.enter() {
			return ErrClosed
		}
		defer b.s.leave()
		if err := b.s.db.Set(k, blk.RawData(), b.s.write); err != nil {
			return fmt.Errorf("pebbleds: set: %w", err)
		}
		b.s.written.Add(int64(len(k) + len(blk.RawData())))
		b.s.blocks.filter(b.s, hashOf(blk.Cid()))
		b.s.blocks.hits.add(hashOf(blk.Cid()))
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
func (b *Blockstore) DeleteBlock(ctx context.Context, c cid.Cid) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return withKey(c, func(k []byte) error {
		if !b.s.enter() {
			return ErrClosed
		}
		defer b.s.leave()
		if err := b.s.db.Delete(k, b.s.write); err != nil {
			return fmt.Errorf("pebbleds: delete: %w", err)
		}
		b.s.blocks.deletes.Add(1)
		b.s.blocks.hits.drop(hashOf(c))
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
