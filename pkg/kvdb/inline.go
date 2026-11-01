package kvdb

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"sync"

	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"github.com/taubyte/tau/pkg/kvdb/statetree"
)

const inlineBudget = 32 << 10

var (
	errStateMismatch  = errors.New("the state root a snapshot carries is not its state")
	errInlineUnlinked = errors.New("a block a snapshot carries is linked by none before it")
	errInlineOversize = errors.New("the blocks a snapshot carries exceed the budget")
)

// carryState sets what lets a reader rebuild sr's StateRoot, and the top of the
// tree blocks written: breadth first from the roots, a block carried only when
// its parent is and it fits in inlineBudget.
func carryState(root *pb.Delta, sr stateRoot, written []ipld.Node) {
	msg := &pb.StateRoot{}
	present, n := sr.message(msg)
	root.Root = msg
	root.Trees = make([][]byte, n)
	for i := range n {
		root.Trees[i] = present[i].Bytes()
	}
	if len(written) == 0 {
		return
	}
	byCid := make(map[cid.Cid]ipld.Node, len(written))
	for _, nd := range written {
		byCid[nd.Cid()] = nd
	}
	queue := append(make([]cid.Cid, 0, 64), present[:n]...)
	size := 0
	for i := 0; i < len(queue); i++ {
		nd, ok := byCid[queue[i]]
		if !ok {
			continue
		}
		raw := nd.RawData()
		if size+len(raw) > inlineBudget {
			continue
		}
		size += len(raw)
		root.Inline = append(root.Inline, raw)
		// A block this replica just wrote decodes; one that did not would
		// have nothing to follow.
		if links, err := blockLinks(nd, queue); err == nil {
			queue = links
		}
	}
}

// A carried is what a snapshot carries of its state, verified: its StateRoot
// block, then its inline tree blocks in order, with the links of each that no
// carried block is.
type carried struct {
	nodes   []ipld.Node
	links   []carriedLink
	cids    []cid.Cid
	fresh   []cid.Cid
	written []ipld.Node
	isNew   []bool
}

type carriedLink struct {
	k      cidKey
	c      cid.Cid
	parent int
}

var carriedPool = sync.Pool{New: func() any { return &carried{} }}

func takeCarried() *carried { return carriedPool.Get().(*carried) }

func (cs *carried) putBack() {
	clear(cs.nodes[:cap(cs.nodes)])
	clear(cs.links[:cap(cs.links)])
	clear(cs.cids[:cap(cs.cids)])
	clear(cs.fresh[:cap(cs.fresh)])
	clear(cs.written[:cap(cs.written)])
	cs.nodes, cs.links, cs.cids, cs.fresh, cs.written, cs.isNew = cs.nodes[:0], cs.links[:0], cs.cids[:0], cs.fresh[:0], cs.written[:0], cs.isNew[:0]
	carriedPool.Put(cs)
}

// verify checks the rebuilt StateRoot is stc and takes each inline block once
// a block before it links it, by its hash, within inlineBudget.
func (cs *carried) verify(stc cid.Cid, root *pb.Delta) error {
	sr, err := carriedRoot(root)
	if err != nil {
		return err
	}
	nd, err := sr.node()
	if err != nil {
		return err
	}
	if nd.Cid() != stc {
		return errStateMismatch
	}
	if err := cs.take(nd); err != nil {
		return err
	}
	size := 0
	for _, raw := range root.GetInline() {
		if size += len(raw); size > inlineBudget {
			return errInlineOversize
		}
		i := cs.linked(sha256.Sum256(raw))
		if i < 0 {
			return errInlineUnlinked
		}
		c := cs.links[i].c
		cs.links = slices.Delete(cs.links, i, i+1)
		nd, err := statetree.ParseBlock(c, raw)
		if err != nil {
			return errMalformedState
		}
		if err := cs.take(nd); err != nil {
			return err
		}
	}
	return nil
}

func carriedRoot(root *pb.Delta) (stateRoot, error) {
	msg := root.GetRoot()
	if msg == nil {
		return stateRoot{}, errStateMismatch
	}
	trees := root.GetTrees()
	return stateRootOf(msg, len(trees), func(i int) (cid.Cid, error) {
		c, err := cid.Cast(trees[i])
		if err == nil && !isStateCid(c) {
			err = errMalformedState
		}
		return c, err
	})
}

func (cs *carried) linked(k cidKey) int {
	for i := range cs.links {
		if cs.links[i].k == k {
			return i
		}
	}
	return -1
}

func (cs *carried) take(nd ipld.Node) error {
	var buf [32]cid.Cid
	links, err := blockLinks(nd, buf[:0])
	if err != nil {
		return errMalformedState
	}
	cs.nodes = append(cs.nodes, nd)
	for _, l := range links {
		if !isStateCid(l) {
			return errMalformedState
		}
		cs.links = append(cs.links, carriedLink{k: keyOf(l), c: l, parent: len(cs.nodes) - 1})
	}
	return nil
}

// seed stages what f's snapshot carries as fetched blocks are staged: what the
// roots do not reach becomes an intent before it is written, and what those
// blocks link is discovered. A failure before they are written ends the
// session for the next attempt.
func (store *Datastore) seed(ctx context.Context, f *stateFetch, cs *carried, held *heldDAG) error {
	f.b = takeFetchBuffers()
	store.gcMu.Lock()
	store.beginSession(f.b.sess)
	store.gcMu.Unlock()
	if err := store.stageCarried(ctx, f, cs, held); err != nil {
		store.releaseFetch(ctx, f)
		return err
	}
	return store.discover(ctx, f, cs.cids)
}

func (store *Datastore) stageCarried(ctx context.Context, f *stateFetch, cs *carried, held *heldDAG) error {
	for _, nd := range cs.nodes {
		cs.cids = append(cs.cids, nd.Cid())
	}
	fresh, err := store.intend(ctx, f.b.sess, cs.cids, cs.fresh[:0])
	cs.fresh = fresh
	if err != nil {
		return err
	}
	for i, nd := range cs.nodes {
		isNew := len(fresh) > 0 && fresh[0] == cs.cids[i]
		if isNew {
			fresh = fresh[1:]
			cs.written = append(cs.written, nd)
		}
		cs.isNew = append(cs.isNew, isNew)
		held.keep(nd)
	}
	if len(cs.written) > 0 {
		if err := store.addBlocks(ctx, cs.written); err != nil {
			return err
		}
	}
	cs.cids = cs.cids[:0]
	for _, l := range cs.links {
		if cs.isNew[l.parent] {
			cs.cids = append(cs.cids, l.c)
		}
	}
	return nil
}
