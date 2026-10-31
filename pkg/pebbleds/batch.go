package pebbleds

import (
	"context"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble/v2"
	ds "github.com/ipfs/go-datastore"
)

// ErrCommitted is returned by a batch used after its Commit.
var ErrCommitted = errors.New("pebbleds: batch already committed")

// Batch collects writes and applies them together on Commit: after a crash
// either all of them are there or none is. The batch copies what it is
// given.
type Batch struct {
	s *Store
	b *pebble.Batch
	// n counts the bytes Set was given.
	n int64
}

var _ ds.Batch = (*Batch)(nil)

// Batch starts a batch. ctx is unused.
func (s *Store) Batch(context.Context) (ds.Batch, error) {
	return s.NewBatch()
}

// NewBatch starts a batch.
func (s *Store) NewBatch() (*Batch, error) {
	if !s.enter() {
		return nil, ErrClosed
	}
	defer s.leave()
	return &Batch{s: s, b: s.db.NewBatch()}, nil
}

// Set stores v at k.
func (b *Batch) Set(k, v []byte) error {
	if b.b == nil {
		return ErrCommitted
	}
	if err := b.b.Set(k, v, nil); err != nil {
		return fmt.Errorf("pebbleds: batch set: %w", err)
	}
	b.n += int64(len(k) + len(v))
	return nil
}

// Remove deletes k.
func (b *Batch) Remove(k []byte) error {
	if b.b == nil {
		return ErrCommitted
	}
	if err := b.b.Delete(k, nil); err != nil {
		return fmt.Errorf("pebbleds: batch delete: %w", err)
	}
	return nil
}

// Put stores value at key.
func (b *Batch) Put(_ context.Context, key ds.Key, value []byte) error {
	return b.Set(keyBytes(key), value)
}

// Delete removes key.
func (b *Batch) Delete(_ context.Context, key ds.Key) error {
	return b.Remove(keyBytes(key))
}

// Commit applies the batch. The batch cannot be used after.
func (b *Batch) Commit(context.Context) error {
	if b.b == nil {
		return ErrCommitted
	}
	if !b.s.enter() {
		return ErrClosed
	}
	defer b.s.leave()
	if err := b.b.Commit(b.s.write); err != nil {
		return fmt.Errorf("pebbleds: commit: %w", err)
	}
	b.s.written.Add(b.n)
	err := b.b.Close()
	b.b, b.n = nil, 0
	return err
}

// Reset empties b, committed or not, so it can be used again.
func (b *Batch) Reset() error {
	if b.b != nil {
		b.b.Reset()
		b.n = 0
		return nil
	}
	if !b.s.enter() {
		return ErrClosed
	}
	defer b.s.leave()
	b.b, b.n = b.s.db.NewBatch(), 0
	return nil
}

// Store is the store b writes.
func (b *Batch) Store() *Store { return b.s }
