package mst

import (
	"context"

	dag "github.com/ipfs/boxo/ipld/merkledag"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
)

// Reachable is every node any of roots reaches. A node's subtrees are its
// block's links, walked by merkledag's walk; a subtree two roots share is
// walked once. The caller collects what it does not hold; this package
// never removes a node.
func Reachable(ctx context.Context, ng ipld.NodeGetter, roots ...cid.Cid) (*cid.Set, error) {
	set := cid.NewSet()
	links := func(ctx context.Context, c cid.Cid) ([]*ipld.Link, error) {
		nd, err := ng.Get(ctx, c)
		if err != nil {
			return nil, err
		}
		return linksOf(nd)
	}
	for _, r := range roots {
		if !r.Defined() {
			continue
		}
		if err := dag.Walk(ctx, links, r, set.Visit); err != nil {
			return nil, err
		}
	}
	return set, nil
}

// Missing is the nodes of the tree at root that ng does not hold and that
// are reached through nodes it does. A tree is complete once Missing returns
// none; until then, fetching these and asking again moves the frontier down.
// complete, if not nil, names nodes the caller already knows are held with
// everything below them, which are not read.
func Missing(ctx context.Context, ng ipld.NodeGetter, root cid.Cid, complete func(cid.Cid) bool) ([]cid.Cid, error) {
	if !root.Defined() {
		return nil, nil
	}
	var missing []cid.Cid
	links := func(ctx context.Context, c cid.Cid) ([]*ipld.Link, error) {
		nd, err := ng.Get(ctx, c)
		if ipld.IsNotFound(err) {
			missing = append(missing, c)
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return linksOf(nd)
	}
	visit := func(c cid.Cid) bool { return complete == nil || !complete(c) }
	if err := dag.Walk(ctx, links, root, visit); err != nil {
		return nil, err
	}
	return missing, nil
}

// linksOf is nd's block's links, as the links merkledag's walk follows.
func linksOf(nd ipld.Node) ([]*ipld.Link, error) {
	_, cids, err := DecodeBlock(nd, nil)
	if err != nil {
		return nil, err
	}
	out := make([]*ipld.Link, len(cids))
	for i, c := range cids {
		out[i] = &ipld.Link{Cid: c}
	}
	return out, nil
}
