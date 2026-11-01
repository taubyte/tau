package kvdb

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	"github.com/taubyte/tau/pkg/pebbleds"
)

// txn collects a merge's writes and commits them as one batch, so a crash
// leaves all of a merge or none. Reads see the pending writes first, since a
// merge decides from what it has decided.
type txn struct {
	store  ds.Datastore
	writes map[string][]byte // nil value: deleted
	order  []string
	// sorted orders the written keys so a scan reads only those under its prefix.
	sorted keyIndex
	steps  int
	// vals holds what reads copied out; a key or value read lives until t is
	// released.
	vals   []byte
	ents   []entry
	elems  []element
	prefix []byte
	// The batches commit writes a pebbleds store with, kept across uses.
	marks, batch *pebbleds.Batch
	// rd is what reads of a pebbleds store go through, made at the first
	// and closed at commit or release: every read sees the store as it was
	// then.
	rd *pebbleds.Reader
}

var txnPool = sync.Pool{New: func() any {
	return &txn{writes: map[string][]byte{}, sorted: keyIndex{root: -1}}
}}

// A map keeps its buckets once cleared, so a txn that held a whole state is
// not kept.
const (
	reusedWrites = 1 << 10
	reusedVals   = 1 << 16
)

func newTxn(store ds.Datastore) *txn {
	t := txnPool.Get().(*txn)
	t.store = store
	return t
}

// Nothing may read or write t after release.
func (t *txn) release() {
	t.endReads()
	if len(t.order) > reusedWrites || cap(t.vals) > reusedVals {
		return
	}
	clear(t.writes)
	clear(t.order)
	t.order = t.order[:0]
	t.sorted = keyIndex{left: t.sorted.left[:0], right: t.sorted.right[:0], root: -1}
	clear(t.ents[:cap(t.ents)])
	clear(t.elems[:cap(t.elems)])
	t.store, t.steps, t.vals = nil, 0, t.vals[:0]
	t.ents, t.elems = t.ents[:0], t.elems[:0]
	txnPool.Put(t)
}

func (t *txn) reader(s *pebbleds.Store) (*pebbleds.Reader, error) {
	if t.rd == nil {
		rd, err := s.NewReader()
		if err != nil {
			return nil, err
		}
		t.rd = rd
	}
	return t.rd, nil
}

func (t *txn) endReads() {
	if t.rd != nil {
		t.rd.Close()
		t.rd = nil
	}
}

// buf is where t's next key or value is appended; keep claims it.
func (t *txn) buf() []byte { return t.vals }

// What keep claims lives until t is released; nothing else may append to buf
// in between.
func (t *txn) keep(b []byte) []byte {
	start := len(t.vals)
	t.vals = b
	return b[start:len(b):len(b)]
}

func (t *txn) key(b []byte) ds.Key { return dsKey(t.keep(b)) }

func (t *txn) put(k ds.Key, v []byte) {
	if v == nil {
		v = []byte{}
	}
	t.set(k.String(), v)
}

func (t *txn) del(k ds.Key) {
	t.set(k.String(), nil)
}

func (t *txn) set(k string, v []byte) {
	if _, ok := t.writes[k]; !ok {
		t.order = append(t.order, k)
		t.sorted.insert(t.order)
	}
	t.writes[k] = v
}

// The value lives until t is released.
func (t *txn) get(ctx context.Context, k ds.Key) ([]byte, error) {
	if v, ok := t.writes[k.String()]; ok {
		if v == nil {
			return nil, ds.ErrNotFound
		}
		return v, nil
	}
	s, ok := below(t.store).(*pebbleds.Store)
	if !ok {
		return t.store.Get(ctx, k)
	}
	rd, err := t.reader(s)
	if err != nil {
		return nil, err
	}
	var out []byte
	err = rd.View(bytesOf(k.String()), func(v []byte) error {
		start := len(t.vals)
		t.vals = append(t.vals, v...)
		out = t.vals[start:len(t.vals):len(t.vals)]
		return nil
	})
	return out, err
}

func (t *txn) has(ctx context.Context, k ds.Key) (bool, error) {
	if v, ok := t.writes[k.String()]; ok {
		return v != nil, nil
	}
	if s, ok := below(t.store).(*pebbleds.Store); ok {
		rd, err := t.reader(s)
		if err != nil {
			return false, err
		}
		err = rd.View(bytesOf(k.String()), func([]byte) error { return nil })
		if errors.Is(err, ds.ErrNotFound) {
			return false, nil
		}
		return err == nil, err
	}
	return t.store.Has(ctx, k)
}

type entry struct {
	key   string
	value []byte
}

// The entries last until the next scan on t; their keys and values until t is
// released.
func (t *txn) scan(ctx context.Context, prefix ds.Key) ([]entry, error) {
	out := t.ents[:0]
	defer func() { t.ents = out[:0] }()
	keep := func(k, v []byte) error {
		start := len(t.vals)
		t.vals = append(append(t.vals, k...), v...)
		mid := start + len(k)
		out = append(out, entry{key: stringOf(t.vals[start:mid]), value: t.vals[mid:len(t.vals):len(t.vals)]})
		return nil
	}
	var err error
	if s, ok := below(t.store).(*pebbleds.Store); ok {
		var rd *pebbleds.Reader
		if rd, err = t.reader(s); err == nil {
			var buf [128]byte
			err = rd.Scan(scanPrefix(buf[:0], prefix), keep)
		}
	} else {
		err = scanBytes(ctx, t.store, prefix, keep)
	}
	if err != nil {
		return nil, err
	}
	byKey := func(a, b entry) int { return strings.Compare(a.key, b.key) }
	if !slices.IsSortedFunc(out, byKey) {
		slices.SortFunc(out, byKey)
	}
	// Pending writes replace what the store holds; a deleted entry is
	// left with an empty key, and an added one sorted in after.
	stored, dropped := len(out), false
	t.steps += t.sorted.each(t.order, prefix.String(), func(k string) {
		v := t.writes[k]
		i, found := slices.BinarySearchFunc(out[:stored], k, func(e entry, k string) int { return strings.Compare(e.key, k) })
		switch {
		case found && v == nil:
			out[i].key, dropped = "", true
		case found:
			out[i].value = v
		case v != nil:
			out = append(out, entry{key: k, value: v})
		}
	})
	if dropped {
		out = slices.DeleteFunc(out, func(e entry) bool { return e.key == "" })
	}
	if len(out) > 0 && !slices.IsSortedFunc(out, byKey) {
		slices.SortFunc(out, byKey)
	}
	return out, nil
}

// A keyIndex is a treap over the positions of an append-only key list,
// ordered by key. Its nodes are positions, so it holds no pointers, and a
// node's priority is a hash of its position, so its shape is a function of
// the keys alone.
type keyIndex struct {
	left, right []int32
	root        int32
}

func treapPriority(n int32) uint64 {
	z := uint64(n) + 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// The last key of keys must not already be in the index.
func (x *keyIndex) insert(keys []string) {
	n := int32(len(keys) - 1)
	x.left, x.right = append(x.left, -1), append(x.right, -1)
	l, r := x.split(keys, x.root, keys[n])
	x.root = x.join(x.join(l, n), r)
}

func (x *keyIndex) split(keys []string, n int32, k string) (int32, int32) {
	if n < 0 {
		return -1, -1
	}
	if keys[n] < k {
		l, r := x.split(keys, x.right[n], k)
		x.right[n] = l
		return n, r
	}
	l, r := x.split(keys, x.left[n], k)
	x.left[n] = r
	return l, n
}

// Every key of a is below every key of b.
func (x *keyIndex) join(a, b int32) int32 {
	if a < 0 {
		return b
	}
	if b < 0 {
		return a
	}
	if treapPriority(a) > treapPriority(b) {
		x.right[a] = x.join(x.right[a], b)
		return a
	}
	x.left[b] = x.join(a, x.left[b])
	return b
}

// Every key under p sorts in [p+"/", p+"0"), '0' being the byte after '/'. It
// returns the nodes visited.
func (x *keyIndex) each(keys []string, p string, fn func(k string)) int {
	return x.walk(keys, x.root, p, fn)
}

func (x *keyIndex) walk(keys []string, n int32, p string, fn func(k string)) int {
	steps := 0
	for n >= 0 {
		steps++
		k := keys[n]
		if cmpAfter(k, p, '/') < 0 {
			n = x.right[n]
			continue
		}
		if cmpAfter(k, p, '0') >= 0 {
			n = x.left[n]
			continue
		}
		steps += x.walk(keys, x.left[n], p, fn)
		fn(k)
		n = x.right[n]
	}
	return steps
}

// cmpAfter compares k with p followed by c, without making that string.
func cmpAfter(k, p string, c byte) int {
	n := min(len(k), len(p))
	if r := strings.Compare(k[:n], p[:n]); r != 0 || len(k) <= len(p) {
		if r == 0 {
			return -1
		}
		return r
	}
	switch {
	case k[len(p)] < c:
		return -1
	case k[len(p)] > c || len(k) > len(p)+1:
		return 1
	}
	return 0
}

func (t *txn) commit(ctx context.Context) error {
	t.endReads()
	if len(t.writes) == 0 {
		return nil
	}
	var j *journal
	under := t.store
	if js, ok := under.(*journaled); ok {
		j, under = js.j, js.Datastore
	}
	if s, ok := under.(*pebbleds.Store); ok {
		return t.commitTo(ctx, s, j)
	}
	b, err := batchOf(ctx, t.store)
	if err != nil {
		return err
	}
	// A go-datastore may keep the key and value it is given, and t's
	// storage is used again once t is released: it is given copies.
	for _, k := range t.order {
		v := t.writes[k]
		if v == nil {
			err = b.Delete(ctx, ds.RawKey(strings.Clone(k)))
		} else {
			err = b.Put(ctx, ds.RawKey(strings.Clone(k)), bytes.Clone(v))
		}
		if err != nil {
			return err
		}
	}
	return b.Commit(ctx)
}

// The marks j asks for, when j is set, are committed and synced first, as
// journaled's batch does.
func (t *txn) commitTo(ctx context.Context, s *pebbleds.Store, j *journal) error {
	var err error
	if t.marks, err = batchFor(t.marks, s); err != nil {
		return err
	}
	if t.batch, err = batchFor(t.batch, s); err != nil {
		return err
	}
	marked := false
	for _, k := range t.order {
		v := t.writes[k]
		if j != nil {
			m, err := t.markTo(s, j, k, v)
			if err != nil {
				return err
			}
			marked = marked || m
		}
		if v == nil {
			err = t.batch.Remove(bytesOf(k))
		} else {
			err = t.batch.Set(bytesOf(k), v)
		}
		if err != nil {
			return err
		}
	}
	if marked {
		if err := t.marks.Commit(ctx); err != nil {
			return err
		}
		if err := s.Sync(ctx, j.root); err != nil {
			return err
		}
	}
	return t.batch.Commit(ctx)
}

func (t *txn) markTo(s *pebbleds.Store, j *journal, k string, v []byte) (bool, error) {
	if rest, ok := strings.CutPrefix(k, j.elems); ok {
		t.prefix = j.appendElemMark(t.prefix[:0], rest)
		return true, t.marks.Set(t.prefix, nil)
	}
	rest, ok := strings.CutPrefix(k, j.pending)
	if !ok {
		return false, nil
	}
	had, old := false, uint64(0)
	err := s.View(bytesOf(k), func(stamps []byte) error {
		b, err := stampsBucket(stamps)
		had, old = true, b
		return err
	})
	if err != nil && !errors.Is(err, ds.ErrNotFound) {
		return false, err
	}
	bs, n, err := pendingBuckets(had, old, v)
	if err != nil {
		return false, err
	}
	for _, b := range bs[:n] {
		t.prefix = j.appendPendingMark(t.prefix[:0], b, rest)
		if err := t.marks.Set(t.prefix, nil); err != nil {
			return false, err
		}
	}
	return n > 0, nil
}

func batchFor(b *pebbleds.Batch, s *pebbleds.Store) (*pebbleds.Batch, error) {
	if b == nil || b.Store() != s {
		return s.NewBatch()
	}
	return b, b.Reset()
}

// A store without batches gets one that applies its writes in order on commit.
func batchOf(ctx context.Context, store ds.Datastore) (ds.Batch, error) {
	if b, ok := store.(ds.Batching); ok {
		return b.Batch(ctx)
	}
	return ds.NewBasicBatch(store), nil
}

func below(store ds.Datastore) ds.Datastore {
	if j, ok := store.(*journaled); ok {
		return j.Datastore
	}
	return store
}

// In no order to rely on; v is valid only until fn returns.
func scanStore(ctx context.Context, store ds.Datastore, prefix ds.Key, fn func(k string, v []byte) error) error {
	return scanBytes(ctx, store, prefix, func(k, v []byte) error { return fn(string(k), v) })
}

func scanPrefix(dst []byte, prefix ds.Key) []byte {
	dst = append(dst, prefix.String()...)
	if len(dst) != 1 {
		dst = append(dst, '/')
	}
	return dst
}

// k is valid only until fn returns.
func scanBytes(ctx context.Context, store ds.Datastore, prefix ds.Key, fn func(k, v []byte) error) error {
	if s, ok := below(store).(*pebbleds.Store); ok {
		var buf [128]byte
		return s.Scan(scanPrefix(buf[:0], prefix), fn)
	}
	results, err := store.Query(ctx, query.Query{Prefix: prefix.String()})
	if err != nil {
		return err
	}
	defer results.Close()
	for {
		r, ok := results.NextSync()
		if !ok {
			return nil
		}
		if r.Error != nil {
			return r.Error
		}
		if err := fn(bytesOf(r.Key), r.Value); err != nil {
			return err
		}
	}
}
