package pebbleds

import (
	"context"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble/v2"
	ds "github.com/ipfs/go-datastore"
)

var ErrCommitted = errors.New("pebbleds: batch already committed")

// After a crash either all of a batch's writes are there or none. The batch
// copies what it is given.
type Batch struct {
	s *Store
	b *pebble.Batch
	// n counts the bytes Set was given; set and deleted record whether it sets or
	// deletes a block's key.
	n            int64
	set, deleted bool
}

var _ ds.Batch = (*Batch)(nil)

func (s *Store) Batch(context.Context) (ds.Batch, error) {
	return s.NewBatch()
}

func (s *Store) NewBatch() (*Batch, error) {
	if !s.enter() {
		return nil, ErrClosed
	}
	defer s.leave()
	return &Batch{s: s, b: s.db.NewBatch()}, nil
}

func (b *Batch) Set(k, v []byte) error {
	if b.b == nil {
		return ErrCommitted
	}
	if err := b.b.Set(k, v, nil); err != nil {
		return fmt.Errorf("pebbleds: batch set: %w", err)
	}
	b.set = b.set || isBlockKey(k)
	b.n += int64(len(k) + len(v))
	return nil
}

func (b *Batch) Remove(k []byte) error {
	if b.b == nil {
		return ErrCommitted
	}
	if err := b.b.Delete(k, nil); err != nil {
		return fmt.Errorf("pebbleds: batch delete: %w", err)
	}
	b.deleted = b.deleted || isBlockKey(k)
	return nil
}

func (b *Batch) Put(ctx context.Context, key ds.Key, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.Set(keyBytes(key), value)
}

func (b *Batch) Delete(ctx context.Context, key ds.Key) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.Remove(keyBytes(key))
}

// The batch cannot be used after Commit.
func (b *Batch) Commit(ctx context.Context) error {
	if b.b == nil {
		return ErrCommitted
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !b.s.enter() {
		return ErrClosed
	}
	defer b.s.leave()
	if err := b.b.Commit(b.s.write); err != nil {
		return fmt.Errorf("pebbleds: commit: %w", err)
	}
	b.s.written.Add(b.n)
	b.s.wrote()
	if b.set {
		b.noteSets()
	}
	if b.deleted {
		b.s.deleted()
	}
	err := b.b.Close()
	b.b, b.n, b.set, b.deleted = nil, 0, false, false
	return err
}

// noteSets runs once the blocks are written.
func (b *Batch) noteSets() {
	r := b.b.Reader()
	for {
		kind, k, _, ok, err := r.Next()
		if err != nil {
			logger.Errorf("reading back a committed batch: %v", err)
			return
		}
		if !ok {
			return
		}
		if kind == pebble.InternalKeyKindSet {
			b.s.noteBlockSet(k)
		}
	}
}

// Reset empties b, committed or not, for reuse.
func (b *Batch) Reset() error {
	if b.b != nil {
		b.b.Reset()
		b.n, b.set, b.deleted = 0, false, false
		return nil
	}
	if !b.s.enter() {
		return ErrClosed
	}
	defer b.s.leave()
	b.b, b.n, b.set, b.deleted = b.s.db.NewBatch(), 0, false, false
	return nil
}

func (b *Batch) Store() *Store { return b.s }
