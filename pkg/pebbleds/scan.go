package pebbleds

import (
	"fmt"
	"sync"

	"github.com/cockroachdb/pebble/v2"
)

// scratch is what one scan needs besides its iterator, kept for the next.
type scratch struct {
	opts         pebble.IterOptions
	lower, upper []byte
}

var scratchPool = sync.Pool{New: func() any { return new(scratch) }}

// Range calls fn with every key in [lower, upper) and its value, in key
// order, until fn returns an error, which Range returns. A nil bound is
// open. It reads the store as it was when it began.
func (s *Store) Range(lower, upper []byte, fn func(k, v []byte) error) error {
	sc := scratchPool.Get().(*scratch)
	defer scratchPool.Put(sc)
	return s.walk(sc, lower, upper, fn)
}

// Scan calls fn with every key starting with prefix and its value, in key
// order, until fn returns an error, which Scan returns. It keeps nothing of
// prefix.
func (s *Store) Scan(prefix []byte, fn func(k, v []byte) error) error {
	sc := scratchPool.Get().(*scratch)
	defer scratchPool.Put(sc)
	sc.lower = append(sc.lower[:0], prefix...)
	upper, ok := successor(sc.upper[:0], prefix)
	sc.upper = upper
	if !ok {
		upper = nil
	}
	return s.walk(sc, sc.lower, upper, fn)
}

func (s *Store) walk(sc *scratch, lower, upper []byte, fn func(k, v []byte) error) (err error) {
	if !s.enter() {
		return ErrClosed
	}
	defer s.leave()
	// An empty lower bound bounds nothing; pebble's race builds read its
	// first byte.
	if len(lower) == 0 {
		lower = nil
	}
	sc.opts = pebble.IterOptions{LowerBound: lower, UpperBound: upper}
	it, err := s.db.NewIter(&sc.opts)
	if err != nil {
		return fmt.Errorf("pebbleds: iterate: %w", err)
	}
	defer func() {
		if cerr := it.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("pebbleds: iterate: %w", cerr)
		}
		sc.opts = pebble.IterOptions{}
	}()
	for ok := it.First(); ok; ok = it.Next() {
		v, verr := it.ValueAndErr()
		if verr != nil {
			return fmt.Errorf("pebbleds: read: %w", verr)
		}
		if err := fn(it.Key(), v); err != nil {
			return err
		}
	}
	return nil
}

// successor appends to dst the least key above every key starting with
// prefix: prefix with its last byte below 0xff incremented and the rest
// dropped. A prefix of only 0xff bytes has none.
func successor(dst, prefix []byte) ([]byte, bool) {
	for i := len(prefix) - 1; i >= 0; i-- {
		if prefix[i] != 0xff {
			dst = append(dst, prefix[:i+1]...)
			dst[i]++
			return dst, true
		}
	}
	return dst, false
}
