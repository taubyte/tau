package statetree

import (
	"bytes"
	"context"
	"slices"
	"strings"

	cid "github.com/ipfs/go-cid"
)

type Kind uint8

const (
	Added Kind = iota + 1
	Removed
	Changed
)

type Change struct {
	Kind     Kind
	Key      []byte
	Old, New []byte
}

type item struct {
	e     entry
	sub   cid.Cid
	below uint32
	n     *node
}

type cursor struct {
	t      *Tree
	stack  []item
	opened []cid.Cid
}

func (c *cursor) reset(t *Tree) {
	c.t, c.stack, c.opened = t, c.stack[:0], c.opened[:0]
	if t.root.Defined() {
		c.stack = append(c.stack, item{sub: t.root, below: noLevel})
	}
}

func (c *cursor) head() *item {
	if len(c.stack) == 0 {
		return nil
	}
	return &c.stack[len(c.stack)-1]
}

func (c *cursor) pop() { c.stack = c.stack[:len(c.stack)-1] }

func (c *cursor) level(ctx context.Context) (uint32, error) {
	h := c.head()
	if h.n == nil {
		n, err := c.t.load(ctx, h.sub, h.below)
		if err != nil {
			return 0, err
		}
		h.n = n
	}
	return h.n.level, nil
}

func (c *cursor) open(ctx context.Context) error {
	if _, err := c.level(ctx); err != nil {
		return err
	}
	h := c.head()
	n, sub := h.n, h.sub
	c.pop()
	c.opened = append(c.opened, sub)
	for i := len(n.entries); i >= 0; i-- {
		if k := n.kids[i]; k.c.Defined() {
			c.stack = append(c.stack, item{sub: k.c, below: n.level})
		}
		if i > 0 {
			c.stack = append(c.stack, item{e: n.entries[i-1]})
		}
	}
	return nil
}

// walk skips subtrees with equal CIDs. Of two that differ it opens the one
// with the higher root level, both when equal, since only the lower can equal
// a subtree of the higher. A subtree facing an entry is opened.
func walk(ctx context.Context, ca, cb *cursor, fn func(Change) error) error {
	for {
		ha, hb := ca.head(), cb.head()
		var err error
		switch {
		case ha == nil && hb == nil:
			return nil
		case ha == nil || hb == nil:
			c, h, kind := ca, ha, Removed
			if ha == nil {
				c, h, kind = cb, hb, Added
			}
			if h.sub.Defined() {
				err = c.open(ctx)
				break
			}
			e := h.e
			c.pop()
			err = emit(fn, kind, e, e)
		case ha.sub.Defined() && hb.sub.Defined():
			if ha.sub == hb.sub {
				ca.pop()
				cb.pop()
				break
			}
			var la, lb uint32
			if la, err = ca.level(ctx); err != nil {
				return err
			}
			if lb, err = cb.level(ctx); err != nil {
				return err
			}
			if la >= lb {
				err = ca.open(ctx)
			}
			if err == nil && lb >= la {
				err = cb.open(ctx)
			}
		case ha.sub.Defined():
			err = ca.open(ctx)
		case hb.sub.Defined():
			err = cb.open(ctx)
		default:
			ea, eb := ha.e, hb.e
			switch bytes.Compare(ea.key, eb.key) {
			case -1:
				ca.pop()
				err = emit(fn, Removed, ea, ea)
			case 1:
				cb.pop()
				err = emit(fn, Added, eb, eb)
			default:
				ca.pop()
				cb.pop()
				if !bytes.Equal(ea.val, eb.val) {
					err = emit(fn, Changed, ea, eb)
				}
			}
		}
		if err != nil {
			return err
		}
	}
}

func emit(fn func(Change) error, kind Kind, old, new entry) error {
	if fn == nil {
		return nil
	}
	ch := Change{Kind: kind, Key: new.key}
	if kind != Added {
		ch.Old = old.val
	}
	if kind != Removed {
		ch.New = new.val
	}
	return fn(ch)
}

// Diff calls fn in key order with each key whose value differs, reading only
// subtrees whose CIDs differ. a and b may be over different DAGServices.
func Diff(ctx context.Context, a, b *Tree, fn func(Change) error) error {
	var ca, cb cursor
	ca.reset(a)
	cb.reset(b)
	return walk(ctx, &ca, &cb, fn)
}

// A peer holding a fetches exactly onlyB to hold b.
func DiffNodes(ctx context.Context, a, b *Tree) (onlyA, onlyB []cid.Cid, err error) {
	var d Differ
	return d.Nodes(ctx, a, b)
}

// A Differ is DiffNodes in storage it reuses: once grown, a call allocates only
// what reading nodes does.
type Differ struct {
	a, b         cursor
	onlyA, onlyB []cid.Cid
}

// The results are valid until d's next call.
func (d *Differ) Nodes(ctx context.Context, a, b *Tree) (onlyA, onlyB []cid.Cid, err error) {
	d.a.reset(a)
	d.b.reset(b)
	// The stacks keep no node once the call is over.
	defer func() {
		clear(d.a.stack[:cap(d.a.stack)])
		clear(d.b.stack[:cap(d.b.stack)])
	}()
	if err := walk(ctx, &d.a, &d.b, nil); err != nil {
		return nil, nil, err
	}
	// Shared subtrees are never opened, so the opened sets alone tell the trees'
	// nodes apart.
	x, y := d.a.opened, d.b.opened
	slices.SortFunc(x, compareCids)
	slices.SortFunc(y, compareCids)
	d.onlyA, d.onlyB = d.onlyA[:0], d.onlyB[:0]
	i, j := 0, 0
	for i < len(x) || j < len(y) {
		switch {
		case j == len(y) || i < len(x) && compareCids(x[i], y[j]) < 0:
			d.onlyA = append(d.onlyA, x[i])
			i++
		case i == len(x) || compareCids(x[i], y[j]) > 0:
			d.onlyB = append(d.onlyB, y[j])
			j++
		default:
			i++
			j++
		}
	}
	return d.onlyA, d.onlyB, nil
}

func compareCids(a, b cid.Cid) int { return strings.Compare(a.KeyString(), b.KeyString()) }
