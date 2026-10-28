package mst

import (
	"bytes"
	"context"
	"errors"

	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
)

// Stop ends a Range or Scan early when its function returns it; the
// iteration then returns nil.
var Stop = errors.New("mst: stop")

// A Tree is an immutable handle on one root. Its methods are safe for
// concurrent use when its DAGService is.
type Tree struct {
	dags ipld.DAGService
	sum  Summarizer
	root cid.Cid
}

type Option func(*Tree)

// WithSummarizer gives every subtree a summary computed by s. A tree must be
// read with the summarizer it was written with.
func WithSummarizer(s Summarizer) Option {
	return func(t *Tree) { t.sum = s }
}

// New is the empty tree over dags.
func New(dags ipld.DAGService, opts ...Option) *Tree {
	return Load(dags, cid.Undef, opts...)
}

// Load is the tree whose root node is root, or the empty tree for cid.Undef.
// It reads nothing.
func Load(dags ipld.DAGService, root cid.Cid, opts ...Option) *Tree {
	t := &Tree{dags: dags, root: root}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Root is the CID of the root node, cid.Undef for the empty tree.
func (t *Tree) Root() cid.Cid { return t.root }

func (t *Tree) with(root cid.Cid) *Tree {
	return &Tree{dags: t.dags, sum: t.sum, root: root}
}

func (t *Tree) load(ctx context.Context, c cid.Cid, below uint32) (*node, error) {
	nd, err := t.dags.Get(ctx, c)
	if err != nil {
		return nil, err
	}
	return decode(nd, below)
}

// Get returns the value of key and whether the tree holds it.
func (t *Tree) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	lvl := Level(key)
	c, below := t.root, uint32(noLevel)
	for c.Defined() {
		n, err := t.load(ctx, c, below)
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
		c, below = n.kids[i].c, n.level
	}
	return nil, false, nil
}

// Summary is the summary of the whole tree, nil when it is empty or has no
// summarizer.
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

// An op collects the nodes one change writes, and adds them together once
// the change is complete, so a failed change writes nothing.
type op struct {
	ctx context.Context
	t   *Tree
	out []ipld.Node
}

func (o *op) load(c cid.Cid, below uint32) (*node, error) {
	return o.t.load(o.ctx, c, below)
}

func (o *op) write(n *node) (ref, error) {
	nd, err := encode(n)
	if err != nil {
		return ref{}, err
	}
	o.out = append(o.out, nd)
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

func (o *op) rootRef() ref {
	return ref{c: o.t.root}
}

// Put returns the tree with key set to value. The tree is unchanged, and
// nothing is written, when it already holds that value.
func (t *Tree) Put(ctx context.Context, key, value []byte) (*Tree, error) {
	o := &op{ctx: ctx, t: t}
	e := entry{key: bytes.Clone(key), val: bytes.Clone(value)}
	if len(e.val) == 0 {
		e.val = nil
	}
	r, changed, err := o.insert(o.rootRef(), noLevel, e, Level(key))
	if err != nil || !changed {
		return t, err
	}
	if err := o.flush(); err != nil {
		return nil, err
	}
	return t.with(r.c), nil
}

func (o *op) single(e entry, lvl uint32, left, right ref) (ref, error) {
	return o.write(&node{level: lvl, entries: []entry{e}, kids: []ref{left, right}})
}

func (o *op) insert(r ref, below uint32, e entry, lvl uint32) (ref, bool, error) {
	if !r.c.Defined() {
		nr, err := o.single(e, lvl, ref{}, ref{})
		return nr, err == nil, err
	}
	n, err := o.load(r.c, below)
	if err != nil {
		return ref{}, false, err
	}
	if lvl > n.level {
		l, rt, err := o.splitNode(n, r, e.key)
		if err != nil {
			return ref{}, false, err
		}
		nr, err := o.single(e, lvl, l, rt)
		return nr, err == nil, err
	}
	i, found := n.search(e.key)
	if found {
		if bytes.Equal(n.entries[i].val, e.val) {
			return r, false, nil
		}
		m := n.copy()
		m.entries[i].val = e.val
		nr, err := o.write(m)
		return nr, err == nil, err
	}
	m := &node{level: n.level}
	if lvl == n.level {
		l, rt, err := o.split(n.kids[i], n.level, e.key)
		if err != nil {
			return ref{}, false, err
		}
		m.entries = append(append(append(make([]entry, 0, len(n.entries)+1), n.entries[:i]...), e), n.entries[i:]...)
		m.kids = append(append(append(make([]ref, 0, len(n.kids)+1), n.kids[:i]...), l, rt), n.kids[i+1:]...)
	} else {
		kid, changed, err := o.insert(n.kids[i], n.level, e, lvl)
		if err != nil || !changed {
			return r, false, err
		}
		m = n.copy()
		m.kids[i] = kid
	}
	nr, err := o.write(m)
	return nr, err == nil, err
}

func (n *node) copy() *node {
	return &node{
		level:   n.level,
		entries: append([]entry(nil), n.entries...),
		kids:    append([]ref(nil), n.kids...),
	}
}

// split divides the subtree r, which does not hold key, into the keys below
// key and those above it.
func (o *op) split(r ref, below uint32, key []byte) (ref, ref, error) {
	if !r.c.Defined() {
		return ref{}, ref{}, nil
	}
	n, err := o.load(r.c, below)
	if err != nil {
		return ref{}, ref{}, err
	}
	return o.splitNode(n, r, key)
}

// splitNode splits n, whose ref is self. A side that keeps all of n is
// self, not a copy of it.
func (o *op) splitNode(n *node, self ref, key []byte) (ref, ref, error) {
	i, _ := n.search(key)
	l, r, err := o.split(n.kids[i], n.level, key)
	if err != nil {
		return ref{}, ref{}, err
	}
	switch {
	case i == 0 && r.c == n.kids[0].c:
		return l, self, nil
	case i == len(n.entries) && l.c == n.kids[i].c:
		return self, r, nil
	}
	if i > 0 {
		kids := append(append(make([]ref, 0, i+1), n.kids[:i]...), l)
		if l, err = o.write(&node{level: n.level, entries: n.entries[:i], kids: kids}); err != nil {
			return ref{}, ref{}, err
		}
	}
	if i < len(n.entries) {
		kids := append(append(make([]ref, 0, len(n.kids)-i), r), n.kids[i+1:]...)
		if r, err = o.write(&node{level: n.level, entries: n.entries[i:], kids: kids}); err != nil {
			return ref{}, ref{}, err
		}
	}
	return l, r, nil
}

// Delete returns the tree without key. The tree is unchanged, and nothing
// is written, when it does not hold key.
func (t *Tree) Delete(ctx context.Context, key []byte) (*Tree, error) {
	o := &op{ctx: ctx, t: t}
	r, changed, err := o.remove(o.rootRef(), noLevel, key, Level(key))
	if err != nil || !changed {
		return t, err
	}
	if err := o.flush(); err != nil {
		return nil, err
	}
	return t.with(r.c), nil
}

func (o *op) remove(r ref, below uint32, key []byte, lvl uint32) (ref, bool, error) {
	if !r.c.Defined() {
		return r, false, nil
	}
	n, err := o.load(r.c, below)
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
		m := n.copy()
		m.kids[i] = kid
		nr, err := o.write(m)
		return nr, err == nil, err
	}
	mid, err := o.merge(n.kids[i], n.kids[i+1], n.level)
	if err != nil {
		return ref{}, false, err
	}
	if len(n.entries) == 1 {
		return mid, true, nil
	}
	m := &node{
		level:   n.level,
		entries: append(append(make([]entry, 0, len(n.entries)-1), n.entries[:i]...), n.entries[i+1:]...),
		kids:    append(append(append(make([]ref, 0, len(n.kids)-1), n.kids[:i]...), mid), n.kids[i+2:]...),
	}
	nr, err := o.write(m)
	return nr, err == nil, err
}

// merge joins two adjacent subtrees, every key of a below every key of b.
func (o *op) merge(a, b ref, below uint32) (ref, error) {
	if !a.c.Defined() {
		return b, nil
	}
	if !b.c.Defined() {
		return a, nil
	}
	na, err := o.load(a.c, below)
	if err != nil {
		return ref{}, err
	}
	nb, err := o.load(b.c, below)
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
		m := na.copy()
		m.kids[last] = mid
		return o.write(m)
	case nb.level > na.level:
		mid, err := o.merge(a, nb.kids[0], nb.level)
		if err != nil {
			return ref{}, err
		}
		m := nb.copy()
		m.kids[0] = mid
		return o.write(m)
	}
	mid, err := o.merge(na.kids[len(na.kids)-1], nb.kids[0], na.level)
	if err != nil {
		return ref{}, err
	}
	m := &node{
		level:   na.level,
		entries: append(append(make([]entry, 0, len(na.entries)+len(nb.entries)), na.entries...), nb.entries...),
		kids:    append(append(append(make([]ref, 0, len(na.kids)+len(nb.kids)-1), na.kids[:len(na.kids)-1]...), mid), nb.kids[1:]...),
	}
	return o.write(m)
}

// Range calls fn with each entry whose key is in [start, end), in key
// order. A nil start or end is unbounded. The slices fn gets are its to
// keep and must not be modified.
func (t *Tree) Range(ctx context.Context, start, end []byte, fn func(key, value []byte) error) error {
	return t.Scan(ctx, start, end, nil, fn)
}

// Prefix calls fn with each entry whose key starts with prefix, in key
// order.
func (t *Tree) Prefix(ctx context.Context, prefix []byte, fn func(key, value []byte) error) error {
	return t.Range(ctx, prefix, prefixEnd(prefix), fn)
}

// prefixEnd is the least key above every key starting with p, nil if there
// is none.
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

// Scan is Range, but it does not enter a subtree whose summary skip
// returns true for. Entries of the nodes it reads still reach fn, so fn
// sees a superset of what skip let through.
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
