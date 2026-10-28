package mst

import (
	"bytes"
	"context"

	cid "github.com/ipfs/go-cid"
)

type Kind uint8

const (
	Added Kind = iota + 1
	Removed
	Changed
)

// A Change is one key that differs between two trees: Old is its value in
// the first, New in the second.
type Change struct {
	Kind     Kind
	Key      []byte
	Old, New []byte
}

// An item is what a cursor has yet to visit: an entry, or a subtree it has
// not opened.
type item struct {
	e     entry
	sub   cid.Cid
	below uint32
	n     *node
}

// A cursor walks one tree in key order, opening a subtree only when asked
// to, and records every node it opens.
type cursor struct {
	t      *Tree
	stack  []item
	opened []cid.Cid
}

func newCursor(t *Tree) *cursor {
	c := &cursor{t: t}
	if t.root.Defined() {
		c.stack = append(c.stack, item{sub: t.root, below: noLevel})
	}
	return c
}

func (c *cursor) head() *item {
	if len(c.stack) == 0 {
		return nil
	}
	return &c.stack[len(c.stack)-1]
}

func (c *cursor) pop() { c.stack = c.stack[:len(c.stack)-1] }

// level reads the head subtree's root, once, for its level.
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

// open replaces the head subtree with its root's entries and subtrees.
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

// walk visits a and b in step. Two subtrees with the same CID hold the same
// entries and are passed over unread; of two that differ, the one with the
// higher root level is opened, both when the levels are equal, since only
// the lower one can equal a subtree of the higher. A subtree facing an entry
// is opened.
func walk(ctx context.Context, a, b *Tree, fn func(Change) error) (*cursor, *cursor, error) {
	ca, cb := newCursor(a), newCursor(b)
	for {
		ha, hb := ca.head(), cb.head()
		var err error
		switch {
		case ha == nil && hb == nil:
			return ca, cb, nil
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
				return nil, nil, err
			}
			if lb, err = cb.level(ctx); err != nil {
				return nil, nil, err
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
			return nil, nil, err
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

// Diff calls fn, in key order, with every key whose value differs between a
// and b: Removed when only a holds it, Added when only b does. It reads only
// the nodes of subtrees whose CIDs differ, which for k changed keys is
// O(k × depth) nodes. The trees may be over different DAGServices.
func Diff(ctx context.Context, a, b *Tree, fn func(Change) error) error {
	_, _, err := walk(ctx, a, b, fn)
	return err
}

// DiffNodes returns the CIDs of the nodes only a reaches and those only b
// reaches. A peer that holds a and is given b's root fetches exactly onlyB.
func DiffNodes(ctx context.Context, a, b *Tree) (onlyA, onlyB []cid.Cid, err error) {
	ca, cb, err := walk(ctx, a, b, nil)
	if err != nil {
		return nil, nil, err
	}
	return minus(ca.opened, cb.opened), minus(cb.opened, ca.opened), nil
}

// minus is x without the CIDs in y. A node either tree reaches is either
// inside a subtree both share, which neither opens, or opened by the walk,
// so the opened sets alone tell the trees' nodes apart.
func minus(x, y []cid.Cid) []cid.Cid {
	in := make(map[cid.Cid]struct{}, len(y))
	for _, c := range y {
		in[c] = struct{}{}
	}
	var out []cid.Cid
	for _, c := range x {
		if _, ok := in[c]; !ok {
			out = append(out, c)
		}
	}
	return out
}
