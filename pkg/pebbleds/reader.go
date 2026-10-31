package pebbleds

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/cockroachdb/pebble/v2"
	ds "github.com/ipfs/go-datastore"
)

// A Reader reads the store as it was when it was made, through one pebble
// iterator: a run of reads sets up one, where each View and Scan sets up its
// own. It is not safe for concurrent use. Close releases what it holds,
// which Close of the store does for a reader left open.
type Reader struct {
	s            *Store
	it           *pebble.Iterator
	bounded      bool
	lower, upper []byte
}

// readerPool keeps closed readers, with the bounds they grew.
var readerPool = sync.Pool{New: func() any { return new(Reader) }}

// NewReader makes a reader of the store as it is now.
func (s *Store) NewReader() (*Reader, error) {
	if !s.enter() {
		return nil, ErrClosed
	}
	defer s.leave()
	it, err := s.db.NewIter(nil)
	if err != nil {
		return nil, fmt.Errorf("pebbleds: iterate: %w", err)
	}
	r := readerPool.Get().(*Reader)
	r.s, r.it = s, it
	s.mu.Lock()
	s.readers[r] = struct{}{}
	s.mu.Unlock()
	return r, nil
}

// View calls fn with the value stored at key, as View of the store does.
func (r *Reader) View(key []byte, fn func(v []byte) error) error {
	if !r.s.enter() {
		return ErrClosed
	}
	defer r.s.leave()
	if r.bounded {
		r.it.SetBounds(nil, nil)
		r.bounded = false
	}
	if !r.it.SeekPrefixGE(key) || !bytes.Equal(r.it.Key(), key) {
		if err := r.it.Error(); err != nil {
			return fmt.Errorf("pebbleds: get: %w", err)
		}
		return ds.ErrNotFound
	}
	v, err := r.it.ValueAndErr()
	if err != nil {
		return fmt.Errorf("pebbleds: get: %w", err)
	}
	return fn(v)
}

// Scan calls fn with every key starting with prefix and its value, as Scan
// of the store does.
func (r *Reader) Scan(prefix []byte, fn func(k, v []byte) error) error {
	if !r.s.enter() {
		return ErrClosed
	}
	defer r.s.leave()
	// The bounds are the reader's own, so that prefix is not kept.
	r.lower = append(r.lower[:0], prefix...)
	upper, ok := successor(r.upper[:0], prefix)
	r.upper = upper
	if !ok {
		upper = nil
	}
	lower := r.lower
	if len(lower) == 0 {
		lower = nil
	}
	r.it.SetBounds(lower, upper)
	r.bounded = true
	for ok := r.it.First(); ok; ok = r.it.Next() {
		v, err := r.it.ValueAndErr()
		if err != nil {
			return fmt.Errorf("pebbleds: read: %w", err)
		}
		if err := fn(r.it.Key(), v); err != nil {
			return err
		}
	}
	if err := r.it.Error(); err != nil {
		return fmt.Errorf("pebbleds: iterate: %w", err)
	}
	return nil
}

// Close releases the reader. Nothing may use it after, Close included.
func (r *Reader) Close() error {
	// A closing store closes the readers left open.
	if !r.s.enter() {
		return nil
	}
	defer r.s.leave()
	r.s.mu.Lock()
	delete(r.s.readers, r)
	r.s.mu.Unlock()
	err := r.it.Close()
	r.s, r.it, r.bounded = nil, nil, false
	readerPool.Put(r)
	if err != nil {
		return fmt.Errorf("pebbleds: iterate: %w", err)
	}
	return nil
}
