package mst

import (
	"bytes"
	"context"
)

// Verify reads the whole tree and checks it is the canonical tree of its
// entries in format profile 1: every node's block is byte for byte the one
// this package would write for it, every key sits at its level, inside the
// slot it is in, and every summary is the one t's summarizer gives. A tree
// from a peer that passes is the tree any writer of the same entries makes.
func (t *Tree) Verify(ctx context.Context) error {
	_, err := t.verify(ctx, ref{c: t.root}, noLevel, nil, nil)
	return err
}

func (t *Tree) verify(ctx context.Context, r ref, below uint32, lo, hi []byte) ([]byte, error) {
	if !r.c.Defined() {
		return nil, nil
	}
	nd, err := t.dags.Get(ctx, r.c)
	if err != nil {
		return nil, err
	}
	n, err := decode(nd, below)
	if err != nil {
		return nil, err
	}
	re, err := encode(n)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(re.RawData(), nd.RawData()) || re.Cid() != r.c {
		return nil, malformed(r.c, "not in canonical form")
	}
	for _, e := range n.entries {
		if Level(e.key) != n.level {
			return nil, malformed(r.c, "key %x is not at level %d", e.key, n.level)
		}
		if (lo != nil && bytes.Compare(e.key, lo) <= 0) || (hi != nil && bytes.Compare(e.key, hi) >= 0) {
			return nil, malformed(r.c, "key %x outside its slot", e.key)
		}
	}
	for i, k := range n.kids {
		klo, khi := lo, hi
		if i > 0 {
			klo = n.entries[i-1].key
		}
		if i < len(n.entries) {
			khi = n.entries[i].key
		}
		sum, err := t.verify(ctx, k, n.level, klo, khi)
		if err != nil {
			return nil, err
		}
		if t.sum != nil && k.c.Defined() && !bytes.Equal(sum, k.sum) {
			return nil, malformed(r.c, "summary of subtree %d is not its entries'", i)
		}
		if t.sum == nil && len(k.sum) > 0 {
			return nil, malformed(r.c, "summary without a summarizer")
		}
	}
	return n.summary(t.sum), nil
}
