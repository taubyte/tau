package kvdb

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	ds "github.com/ipfs/go-datastore"
)

// The journal names every state record changed since the state's trees
// were last brought up to date with the store (see stateBase), so a
// compaction applies exactly those to the trees rather than rebuilding them
// from a scan of the whole state:
//
//	/j/e/<key segment>/<dot segment>           an element
//	/j/t/<bucket>/<dot segment>/<key segment>  a pending removal, under the
//	                                           bucket of an expiry it had
//
// Every write of a record marks it, durably, ahead of the write, so a crash
// in between leaves at worst a mark for a record that did not change, which
// the next compaction finds unchanged, even on a store that keeps any
// subset of the writes no Sync covered. A pending removal is
// marked under the bucket it had before the write and the one it has after,
// since its tree key is its bucket's.
const journalNs = "j"

type journal struct {
	inner                            ds.Datastore
	root                             ds.Key
	elems, pending, jElems, jPending string
}

func newJournal(inner ds.Datastore, namespace ds.Key, set *set) *journal {
	j := namespace.ChildString(journalNs)
	return &journal{
		inner:    inner,
		root:     j,
		elems:    set.elemsPrefix().String() + "/",
		pending:  set.pendingPrefix().String() + "/",
		jElems:   j.ChildString("e").String() + "/",
		jPending: j.ChildString("t").String() + "/",
	}
}

func (j *journal) elemsPrefix() ds.Key   { return ds.RawKey(strings.TrimSuffix(j.jElems, "/")) }
func (j *journal) pendingPrefix() ds.Key { return ds.RawKey(strings.TrimSuffix(j.jPending, "/")) }

func (j *journal) elemMark(key string, d dot) string {
	return j.jElems + keySegment(key) + "/" + d.segment()
}

func (j *journal) pendingMarkIn(b uint64, key string, d dot) string {
	return j.jPending + fmt.Sprintf("%020d", b) + "/" + d.segment() + "/" + keySegment(key)
}

func (j *journal) pendingMark(rest string, stamps []byte) (string, error) {
	at, ttl, err := decodeStamps(stamps)
	if err != nil {
		return "", err
	}
	b := make([]byte, 0, len(j.jPending)+counterDigits+1+len(rest))
	b = append(append(appendCounter(append(b, j.jPending...), bucket(at+ttl)), '/'), rest...)
	return unsafe.String(unsafe.SliceData(b), len(b)), nil
}

func (j *journal) marks(ctx context.Context, key string, value []byte) ([]string, error) {
	var out []string
	err := j.eachMark(ctx, key, value, func(m string) error {
		out = append(out, m)
		return nil
	})
	return out, err
}

// eachMark calls fn with each journal mark a write of value under key makes,
// a nil value being a delete.
func (j *journal) eachMark(ctx context.Context, key string, value []byte, fn func(string) error) error {
	if rest, ok := strings.CutPrefix(key, j.elems); ok {
		return fn(j.jElems + rest)
	}
	rest, ok := strings.CutPrefix(key, j.pending)
	if !ok {
		return nil
	}
	var first string
	old, err := j.inner.Get(ctx, ds.RawKey(key))
	switch {
	case err == nil:
		if first, err = j.pendingMark(rest, old); err != nil {
			return err
		}
		if err := fn(first); err != nil {
			return err
		}
	case !errors.Is(err, ds.ErrNotFound):
		return err
	}
	if value != nil {
		m, err := j.pendingMark(rest, value)
		if err != nil {
			return err
		}
		if m != first {
			return fn(m)
		}
	}
	return nil
}

// parseElemMark reads the element an element mark names.
func (j *journal) parseElemMark(k string) (string, dot, error) {
	kseg, dseg, ok := strings.Cut(strings.TrimPrefix(k, j.jElems), "/")
	if !ok {
		return "", dot{}, fmt.Errorf("bad journal key %q", k)
	}
	key, err := parseKeySegment(kseg)
	if err != nil {
		return "", dot{}, err
	}
	d, err := parseDotSegment(dseg)
	return key, d, err
}

// parsePendingMark reads the bucket and removal a pending mark names.
func (j *journal) parsePendingMark(k string) (uint64, string, dot, error) {
	parts := strings.Split(strings.TrimPrefix(k, j.jPending), "/")
	if len(parts) != 3 {
		return 0, "", dot{}, fmt.Errorf("bad journal key %q", k)
	}
	b, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, "", dot{}, err
	}
	d, err := parseDotSegment(parts[1])
	if err != nil {
		return 0, "", dot{}, err
	}
	key, err := parseKeySegment(parts[2])
	return b, key, d, err
}

// journaled is the store a Datastore writes through: every write of a
// state record also marks it in the journal.
type journaled struct {
	ds.Datastore
	j *journal
}

func (s *journaled) Put(ctx context.Context, key ds.Key, value []byte) error {
	if err := s.markDurably(ctx, key, value); err != nil {
		return err
	}
	return s.Datastore.Put(ctx, key, value)
}

func (s *journaled) Delete(ctx context.Context, key ds.Key) error {
	if err := s.markDurably(ctx, key, nil); err != nil {
		return err
	}
	return s.Datastore.Delete(ctx, key)
}

func (s *journaled) markDurably(ctx context.Context, key ds.Key, value []byte) error {
	marked := false
	if err := s.j.eachMark(ctx, key.String(), value, func(m string) error {
		marked = true
		return s.Datastore.Put(ctx, ds.RawKey(m), []byte{})
	}); err != nil || !marked {
		return err
	}
	return s.Datastore.Sync(ctx, s.j.root)
}

// Batch is a batch whose marks are committed, in a batch of their own, and
// synced before its writes: a store's batch need not apply its writes in
// order, nor keep them in order once committed.
func (s *journaled) Batch(ctx context.Context) (ds.Batch, error) {
	b, err := batchOf(ctx, s.Datastore)
	if err != nil {
		return nil, err
	}
	marks, err := batchOf(ctx, s.Datastore)
	if err != nil {
		return nil, err
	}
	return &journaledBatch{Batch: b, marks: marks, j: s.j}, nil
}

type journaledBatch struct {
	ds.Batch
	marks  ds.Batch
	marked bool
	j      *journal
}

func (b *journaledBatch) mark(ctx context.Context, key ds.Key, value []byte) error {
	return b.j.eachMark(ctx, key.String(), value, func(m string) error {
		b.marked = true
		return b.marks.Put(ctx, ds.RawKey(m), []byte{})
	})
}

func (b *journaledBatch) Commit(ctx context.Context) error {
	if b.marked {
		if err := b.marks.Commit(ctx); err != nil {
			return err
		}
		if err := b.j.inner.Sync(ctx, b.j.root); err != nil {
			return err
		}
	}
	return b.Batch.Commit(ctx)
}

func (b *journaledBatch) Put(ctx context.Context, key ds.Key, value []byte) error {
	if err := b.mark(ctx, key, value); err != nil {
		return err
	}
	return b.Batch.Put(ctx, key, value)
}

func (b *journaledBatch) Delete(ctx context.Context, key ds.Key) error {
	if err := b.mark(ctx, key, nil); err != nil {
		return err
	}
	return b.Batch.Delete(ctx, key)
}
