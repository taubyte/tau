package statetree

import (
	"bytes"
	"context"
	"errors"
	"slices"

	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
)

// Stop ends a Range or Scan early; the iteration then returns nil.
var Stop = errors.New("statetree: stop")

// A Tree is immutable and safe for concurrent use when its DAGService is.
type Tree struct {
	dags  ipld.DAGService
	sum   Summarizer
	cache *Cache
	root  cid.Cid
}

type Option func(*Tree)

// A tree must be read with the summarizer it was written with.
func WithSummarizer(s Summarizer) Option {
	return func(t *Tree) { t.sum = s }
}

func New(dags ipld.DAGService, opts ...Option) *Tree {
	return Load(dags, cid.Undef, opts...)
}

// Load reads nothing; cid.Undef is the empty tree.
func Load(dags ipld.DAGService, root cid.Cid, opts ...Option) *Tree {
	t := &Tree{dags: dags, root: root}
	for _, o := range opts {
		o(t)
	}
	return t
}

func (t *Tree) Root() cid.Cid { return t.root }

// A tree whose DAG service is a BlockReader reads its nodes as stored blocks,
// without IPLD decoding.
type BlockReader interface {
	ReadBlock(ctx context.Context, c cid.Cid) (blocks.Block, error)
}

func (t *Tree) read(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	if r, ok := t.dags.(BlockReader); ok {
		return r.ReadBlock(ctx, c)
	}
	return t.dags.Get(ctx, c)
}

func (t *Tree) with(root cid.Cid) *Tree {
	return &Tree{dags: t.dags, sum: t.sum, cache: t.cache, root: root}
}

func (t *Tree) load(ctx context.Context, c cid.Cid, below uint32) (*node, error) {
	if t.cache == nil {
		b, err := t.read(ctx, c)
		if err != nil {
			return nil, err
		}
		return decode(b, below)
	}
	if n := t.cache.get(c); n != nil {
		if n.level >= below {
			return nil, malformed(c, "level %d under level %d", n.level, below)
		}
		return n, nil
	}
	b, err := t.read(ctx, c)
	if err != nil {
		return nil, err
	}
	n, cost, err := decodeNode(b, below, func(level uint32, size int) bool {
		return t.cache.wants(level, size, below == noLevel)
	})
	if err == nil && cost > 0 {
		t.cache.add(c, n, cost)
	}
	return n, err
}

func (t *Tree) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	o := op{ctx: ctx, t: t}
	return o.get(o.rootRef(), key)
}

// Summary is nil for an empty tree or one with no summarizer.
func (t *Tree) Summary(ctx context.Context) ([]byte, error) {
	if t.sum == nil || !t.root.Defined() {
		return nil, nil
	}
	n, err := t.load(ctx, t.root, noLevel)
	if err != nil {
		return nil, err
	}
	return n.summary(t.sum), nil
}

// An op keeps the nodes it changes in memory, unencoded, until done encodes
// each once and adds them together, so a failed change writes nothing.
type op struct {
	ctx context.Context
	t   *Tree
	out []ipld.Node
	// seen holds the nodes an Editor read; nil for a single change.
	seen map[cid.Cid]*node
}

func (o *op) load(r ref, below uint32) (*node, error) {
	if r.n != nil {
		return r.n, nil
	}
	if n, ok := o.seen[r.c]; ok {
		return n, nil
	}
	n, err := o.t.load(o.ctx, r.c, below)
	if err == nil && o.seen != nil {
		o.seen[r.c] = n
	}
	return n, err
}

// own returns n itself when r holds it in memory, since nothing else reaches
// it, and a copy otherwise.
func (o *op) own(r ref, n *node) *node {
	if r.n == n {
		return n
	}
	return n.copy()
}

func (o *op) write(n *node) (ref, error) {
	nd, err := encode(n)
	if err != nil {
		return ref{}, err
	}
	o.out = append(o.out, nd)
	return ref{c: nd.Cid(), sum: n.summary(o.t.sum)}, nil
}

// finish encodes the in-memory nodes under r, children first. A root's summary
// is not kept.
func (o *op) finish(r ref, root bool) (ref, error) {
	n := r.n
	if n == nil {
		return r, nil
	}
	for i, k := range n.kids {
		if k.n != nil {
			fk, err := o.finish(k, false)
			if err != nil {
				return ref{}, err
			}
			n.kids[i] = fk
		}
	}
	nd, err := encode(n)
	if err != nil {
		return ref{}, err
	}
	o.out = append(o.out, nd)
	if root {
		return ref{c: nd.Cid()}, nil
	}
	return ref{c: nd.Cid(), sum: n.summary(o.t.sum)}, nil
}

func (o *op) flush() error {
	if len(o.out) == 0 {
		return nil
	}
	err := o.t.dags.AddMany(o.ctx, o.out)
	clear(o.out)
	o.out = o.out[:0]
	return err
}

func (o *op) done(r ref) (*Tree, error) {
	fr, err := o.finish(r, true)
	if err != nil {
		return nil, err
	}
	if err := o.flush(); err != nil {
		return nil, err
	}
	return o.t.with(fr.c), nil
}

func (o *op) rootRef() ref {
	return ref{c: o.t.root}
}

// Put writes nothing when the tree already holds value at key.
func (t *Tree) Put(ctx context.Context, key, value []byte) (*Tree, error) {
	o := &op{ctx: ctx, t: t}
	r, changed, err := o.insert(o.rootRef(), noLevel, newEntry(key, value), Level(key))
	if err != nil || !changed {
		return t, err
	}
	return o.done(r)
}

func newEntry(key, value []byte) entry {
	e := entry{key: bytes.Clone(key), val: bytes.Clone(value)}
	if len(e.val) == 0 {
		e.val = nil
	}
	return e
}

func (o *op) single(e entry, lvl uint32, left, right ref) ref {
	return ref{n: &node{level: lvl, entries: []entry{e}, kids: []ref{left, right}}}
}

func (o *op) insert(r ref, below uint32, e entry, lvl uint32) (ref, bool, error) {
	if !r.present() {
		return o.single(e, lvl, ref{}, ref{}), true, nil
	}
	n, err := o.load(r, below)
	if err != nil {
		return ref{}, false, err
	}
	if lvl > n.level {
		l, rt, err := o.splitNode(n, r, e.key)
		if err != nil {
			return ref{}, false, err
		}
		return o.single(e, lvl, l, rt), true, nil
	}
	i, found := n.search(e.key)
	if found {
		if bytes.Equal(n.entries[i].val, e.val) {
			return r, false, nil
		}
		m := o.own(r, n)
		m.entries[i].val = e.val
		return ref{n: m}, true, nil
	}
	if lvl == n.level {
		l, rt, err := o.split(n.kids[i], n.level, e.key)
		if err != nil {
			return ref{}, false, err
		}
		// A node held in memory is changed in place: nothing else reaches it.
		if r.n == n {
			n.entries = slices.Insert(n.entries, i, e)
			n.kids = slices.Replace(n.kids, i, i+1, l, rt)
			return ref{n: n}, true, nil
		}
		return ref{n: &node{
			level:   n.level,
			entries: append(append(append(make([]entry, 0, len(n.entries)+1), n.entries[:i]...), e), n.entries[i:]...),
			kids:    append(append(append(make([]ref, 0, len(n.kids)+1), n.kids[:i]...), l, rt), n.kids[i+1:]...),
		}}, true, nil
	}
	kid, changed, err := o.insert(n.kids[i], n.level, e, lvl)
	if err != nil || !changed {
		return r, false, err
	}
	m := o.own(r, n)
	m.kids[i] = kid
	return ref{n: m}, true, nil
}

func (n *node) copy() *node {
	return &node{
		level:   n.level,
		entries: append([]entry(nil), n.entries...),
		kids:    append([]ref(nil), n.kids...),
	}
}

// split divides r, which must not hold key, at key.
func (o *op) split(r ref, below uint32, key []byte) (ref, ref, error) {
	if !r.present() {
		return ref{}, ref{}, nil
	}
	n, err := o.load(r, below)
	if err != nil {
		return ref{}, ref{}, err
	}
	return o.splitNode(n, r, key)
}

// A side that keeps all of n is self, not a copy, with n's summary; a root's
// ref has none.
func (o *op) splitNode(n *node, self ref, key []byte) (ref, ref, error) {
	i, _ := n.search(key)
	l, r, err := o.split(n.kids[i], n.level, key)
	if err != nil {
		return ref{}, ref{}, err
	}
	whole := func() ref {
		if self.sum == nil && self.n == nil {
			self.sum = n.summary(o.t.sum)
		}
		return self
	}
	switch {
	case i == 0 && r.is(n.kids[0]):
		return l, whole(), nil
	case i == len(n.entries) && l.is(n.kids[i]):
		return whole(), r, nil
	}
	if i > 0 {
		l = ref{n: &node{
			level:   n.level,
			entries: append([]entry(nil), n.entries[:i]...),
			kids:    append(append(make([]ref, 0, i+1), n.kids[:i]...), l),
		}}
	}
	if i < len(n.entries) {
		r = ref{n: &node{
			level:   n.level,
			entries: append([]entry(nil), n.entries[i:]...),
			kids:    append(append(make([]ref, 0, len(n.kids)-i), r), n.kids[i+1:]...),
		}}
	}
	return l, r, nil
}

// Delete writes nothing when the tree does not hold key.
func (t *Tree) Delete(ctx context.Context, key []byte) (*Tree, error) {
	o := &op{ctx: ctx, t: t}
	r, changed, err := o.remove(o.rootRef(), noLevel, key, Level(key))
	if err != nil || !changed {
		return t, err
	}
	return o.done(r)
}

func (o *op) remove(r ref, below uint32, key []byte, lvl uint32) (ref, bool, error) {
	if !r.present() {
		return r, false, nil
	}
	n, err := o.load(r, below)
	if err != nil {
		return ref{}, false, err
	}
	if lvl > n.level {
		return r, false, nil
	}
	i, found := n.search(key)
	if !found {
		if lvl == n.level {
			return r, false, nil
		}
		kid, changed, err := o.remove(n.kids[i], n.level, key, lvl)
		if err != nil || !changed {
			return r, false, err
		}
		m := o.own(r, n)
		m.kids[i] = kid
		return ref{n: m}, true, nil
	}
	mid, err := o.merge(n.kids[i], n.kids[i+1], n.level)
	if err != nil {
		return ref{}, false, err
	}
	if len(n.entries) == 1 {
		return mid, true, nil
	}
	if r.n == n {
		n.entries = slices.Delete(n.entries, i, i+1)
		n.kids = slices.Replace(n.kids, i, i+2, mid)
		return ref{n: n}, true, nil
	}
	return ref{n: &node{
		level:   n.level,
		entries: append(append(make([]entry, 0, len(n.entries)-1), n.entries[:i]...), n.entries[i+1:]...),
		kids:    append(append(append(make([]ref, 0, len(n.kids)-1), n.kids[:i]...), mid), n.kids[i+2:]...),
	}}, true, nil
}

// merge joins adjacent subtrees, every key of a below every key of b.
func (o *op) merge(a, b ref, below uint32) (ref, error) {
	if !a.present() {
		return b, nil
	}
	if !b.present() {
		return a, nil
	}
	na, err := o.load(a, below)
	if err != nil {
		return ref{}, err
	}
	nb, err := o.load(b, below)
	if err != nil {
		return ref{}, err
	}
	switch {
	case na.level > nb.level:
		last := len(na.kids) - 1
		mid, err := o.merge(na.kids[last], b, na.level)
		if err != nil {
			return ref{}, err
		}
		m := o.own(a, na)
		m.kids[last] = mid
		return ref{n: m}, nil
	case nb.level > na.level:
		mid, err := o.merge(a, nb.kids[0], nb.level)
		if err != nil {
			return ref{}, err
		}
		m := o.own(b, nb)
		m.kids[0] = mid
		return ref{n: m}, nil
	}
	mid, err := o.merge(na.kids[len(na.kids)-1], nb.kids[0], na.level)
	if err != nil {
		return ref{}, err
	}
	return ref{n: &node{
		level:   na.level,
		entries: append(append(make([]entry, 0, len(na.entries)+len(nb.entries)), na.entries...), nb.entries...),
		kids:    append(append(append(make([]ref, 0, len(na.kids)+len(nb.kids)-1), na.kids[:len(na.kids)-1]...), mid), nb.kids[1:]...),
	}}, nil
}

// An Editor applies a run of changes, reading and encoding each node once when
// Tree is called. It is not safe for concurrent use and must not be used after
// a method fails.
type Editor struct {
	o    op
	root ref
}

func (t *Tree) Edit() *Editor {
	return &Editor{o: op{t: t, seen: map[cid.Cid]*node{}}, root: ref{c: t.root}}
}

// The Editor keeps key and value, which must not change until Tree returns.
func (e *Editor) Put(ctx context.Context, key, value []byte) error {
	e.o.ctx = ctx
	if len(value) == 0 {
		value = nil
	}
	r, changed, err := e.o.insert(e.root, noLevel, entry{key: key, val: value}, Level(key))
	if err == nil && changed {
		e.root = r
	}
	return err
}

func (e *Editor) Delete(ctx context.Context, key []byte) error {
	e.o.ctx = ctx
	r, changed, err := e.o.remove(e.root, noLevel, key, Level(key))
	if err == nil && changed {
		e.root = r
	}
	return err
}

func (e *Editor) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	e.o.ctx = ctx
	return e.o.get(e.root, key)
}

// The Editor goes on from the tree Tree returns.
func (e *Editor) Tree(ctx context.Context) (*Tree, error) {
	e.o.ctx = ctx
	t, err := e.o.done(e.root)
	if err != nil {
		return nil, err
	}
	e.root = ref{c: t.root}
	return t, nil
}

func (o *op) get(r ref, key []byte) ([]byte, bool, error) {
	lvl := Level(key)
	below := uint32(noLevel)
	for r.present() {
		n, err := o.load(r, below)
		if err != nil {
			return nil, false, err
		}
		if lvl > n.level {
			break
		}
		i, found := n.search(key)
		if found {
			return n.entries[i].val, true, nil
		}
		if lvl == n.level {
			break
		}
		r, below = n.kids[i], n.level
	}
	return nil, false, nil
}

// Range's bounds are [start, end), nil for unbounded. fn may keep the slices
// but must not modify them.
func (t *Tree) Range(ctx context.Context, start, end []byte, fn func(key, value []byte) error) error {
	return t.Scan(ctx, start, end, nil, fn)
}

func (t *Tree) Prefix(ctx context.Context, prefix []byte, fn func(key, value []byte) error) error {
	return t.Range(ctx, prefix, prefixEnd(prefix), fn)
}

func prefixEnd(p []byte) []byte {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] != 0xff {
			end := bytes.Clone(p[:i+1])
			end[i]++
			return end
		}
	}
	return nil
}

// Scan is Range without entering a subtree whose summary skip accepts. Entries
// of the nodes it reads still reach fn, a superset of what skip lets through.
func (t *Tree) Scan(ctx context.Context, start, end []byte, skip func(summary []byte) bool, fn func(key, value []byte) error) error {
	err := t.scan(ctx, t.root, noLevel, start, end, skip, fn)
	if errors.Is(err, Stop) {
		return nil
	}
	return err
}

func (t *Tree) scan(ctx context.Context, c cid.Cid, below uint32, start, end []byte, skip func([]byte) bool, fn func(key, value []byte) error) error {
	if !c.Defined() {
		return nil
	}
	n, err := t.load(ctx, c, below)
	if err != nil {
		return err
	}
	i := 0
	if start != nil {
		i, _ = n.search(start)
	}
	for ; i <= len(n.entries); i++ {
		if i > 0 && end != nil && bytes.Compare(n.entries[i-1].key, end) >= 0 {
			return nil
		}
		if k := n.kids[i]; k.c.Defined() && (skip == nil || !skip(k.sum)) {
			if err := t.scan(ctx, k.c, n.level, start, end, skip, fn); err != nil {
				return err
			}
		}
		if i == len(n.entries) {
			return nil
		}
		e := n.entries[i]
		if end != nil && bytes.Compare(e.key, end) >= 0 {
			return nil
		}
		if start == nil || bytes.Compare(e.key, start) >= 0 {
			if err := fn(e.key, e.val); err != nil {
				return err
			}
		}
	}
	return nil
}
