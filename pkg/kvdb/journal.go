package kvdb

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

func (j *journal) view(ctx context.Context) func(k []byte, fn func(v []byte) error) error {
	return func(k []byte, fn func(v []byte) error) error {
		v, err := j.inner.Get(ctx, ds.RawKey(string(k)))
		if err != nil {
			return err
		}
		return fn(v)
	}
}

// appendMarks hands set each mark a write of value under key makes, a nil
// value being a delete: an element's, or a pending removal's under its buckets
// before and after. A mark is built in *buf and lent for the call. It reports
// whether there was any.
func (j *journal) appendMarks(buf *[]byte, key string, value []byte, view func(k []byte, fn func(v []byte) error) error, set func(m []byte) error) (bool, error) {
	if rest, ok := strings.CutPrefix(key, j.elems); ok {
		*buf = j.appendElemMark((*buf)[:0], rest)
		return true, set(*buf)
	}
	rest, ok := strings.CutPrefix(key, j.pending)
	if !ok {
		return false, nil
	}
	had, old := false, uint64(0)
	err := view(bytesOf(key), func(v []byte) error {
		b, err := stampsBucket(v)
		had, old = true, b
		return err
	})
	if err != nil && !errors.Is(err, ds.ErrNotFound) {
		return false, err
	}
	bs, n, err := pendingBuckets(had, old, value)
	if err != nil {
		return false, err
	}
	for _, b := range bs[:n] {
		*buf = j.appendPendingMark((*buf)[:0], b, rest)
		if err := set(*buf); err != nil {
			return false, err
		}
	}
	return n > 0, nil
}

func (j *journal) appendElemMark(b []byte, rest string) []byte {
	return append(append(b, j.jElems...), rest...)
}

func (j *journal) appendPendingMark(b []byte, bk uint64, rest string) []byte {
	return append(append(appendCounter(append(b, j.jPending...), bk), '/'), rest...)
}

// A pending removal is marked under the bucket it had and, if different, the
// one value gives it; a nil value is a delete.
func pendingBuckets(had bool, old uint64, value []byte) ([2]uint64, int, error) {
	var bs [2]uint64
	n := 0
	if had {
		bs[n] = old
		n++
	}
	if value != nil {
		b, err := stampsBucket(value)
		if err != nil {
			return bs, 0, err
		}
		if !had || b != old {
			bs[n] = b
			n++
		}
	}
	return bs, n, nil
}

func stampsBucket(v []byte) (uint64, error) {
	at, ttl, err := decodeStamps(v)
	return bucket(at + ttl), err
}

// journaled marks every write of a state record in the journal.
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
	var buf []byte
	marked, err := s.j.appendMarks(&buf, key.String(), value, s.j.view(ctx), func(m []byte) error {
		return s.Datastore.Put(ctx, ds.RawKey(string(m)), []byte{})
	})
	if err != nil || !marked {
		return err
	}
	return s.Datastore.Sync(ctx, s.j.root)
}

// The marks are committed in a batch of their own and synced before the
// writes: a store's batch need not apply or keep its writes in order.
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
	var buf []byte
	marked, err := b.j.appendMarks(&buf, key.String(), value, b.j.view(ctx), func(m []byte) error {
		return b.marks.Put(ctx, ds.RawKey(string(m)), []byte{})
	})
	b.marked = b.marked || marked
	return err
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
