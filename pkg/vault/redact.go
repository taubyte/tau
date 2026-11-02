package vault

import (
	"bytes"
	"io"
	"slices"
	"sync"
)

// Mask is what a value is written as.
const Mask = "***"

// Redactor masks every value it was told about in what passes through it.
//
// A value can arrive split across writes, so the last bytes of what has passed, one
// fewer than the longest value, are held until the next write or Flush shows they are
// not the start of one.
type Redactor struct {
	mu      sync.Mutex
	w       io.Writer
	values  [][]byte
	longest int
	held    []byte
}

// NewRedactor masks what is written to w.
func NewRedactor(w io.Writer) *Redactor { return &Redactor{w: w} }

// Add masks value from now on, and each line of it, since a log prints a multi-line
// value a line at a time.
func (r *Redactor) Add(value []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.add(value)
	for line := range bytes.Lines(value) {
		r.add(bytes.TrimRight(line, "\r\n"))
	}
}

func (r *Redactor) add(value []byte) {
	if len(value) == 0 {
		return
	}
	for _, have := range r.values {
		if bytes.Equal(have, value) {
			return
		}
	}
	// Longest first, so a value inside another (a line of a multi-line one) cannot
	// break the longer one's match and leave its rest in the clear.
	at, _ := slices.BinarySearchFunc(r.values, len(value), func(have []byte, n int) int { return n - len(have) })
	r.values = slices.Insert(r.values, at, bytes.Clone(value))
	r.longest = max(r.longest, len(value))
}

func (r *Redactor) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.values) == 0 {
		return r.w.Write(p)
	}

	r.held = append(r.held, p...)
	for _, value := range r.values {
		if bytes.Contains(r.held, value) {
			r.held = bytes.ReplaceAll(r.held, value, []byte(Mask))
		}
	}

	keep := min(r.longest-1, len(r.held))
	ready := len(r.held) - keep
	if ready == 0 {
		return len(p), nil
	}
	_, err := r.w.Write(r.held[:ready])
	r.held = r.held[:copy(r.held, r.held[ready:])]
	return len(p), err
}

// Flush writes what is held.
func (r *Redactor) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.held) == 0 {
		return nil
	}
	_, err := r.w.Write(r.held)
	r.held = r.held[:0]
	return err
}
