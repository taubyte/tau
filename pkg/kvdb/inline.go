package kvdb

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"sync"

	dag "github.com/ipfs/boxo/ipld/merkledag"
	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// inlineBudget bounds the bytes of the tree blocks a snapshot carries.
const inlineBudget = 32 << 10

var (
	errStateMismatch  = errors.New("the state root a snapshot carries is not its state")
	errInlineUnlinked = errors.New("a block a snapshot carries is linked by none before it")
	errInlineOversize = errors.New("the blocks a snapshot carries exceed the budget")
)

// carryState sets in root, the snapshot of the state sr, what lets a
// reader rebuild sr's StateRoot block, and the top of the tree blocks the
// snapshot wrote, of written: breadth first from the trees' roots, a block
// carried only when its parent is and when it fits in what inlineBudget
// leaves.
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
		for _, l := range nd.Links() {
			queue = append(queue, l.Cid)
		}
	}
}

// A carried is what a snapshot carries of its state, verified: its
// StateRoot block, then the tree blocks it carries, in order, and the links
// of each that no block carried is.
type carried struct {
	nodes   []ipld.Node
	links   []carriedLink
	cids    []cid.Cid
	fresh   []cid.Cid
	written []ipld.Node
	isNew   []bool
}

// A carriedLink is a link of nodes[parent].
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

// verify rebuilds the StateRoot block the snapshot root carries and checks
// it is stc, then takes each block root carries inline once a block before
// it links it, by its hash, within inlineBudget.
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
		blk, err := blocks.NewBlockWithCid(raw, c)
		if err != nil {
			return err
		}
		nd, err := dag.DecodeProtobufBlock(blk)
		if err != nil {
			return err
		}
		if err := cs.take(nd); err != nil {
			return err
		}
	}
	return nil
}

// carriedRoot is the state whose StateRoot the snapshot root carries.
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

// linked is the index of the first link to the block k names, or -1.
func (cs *carried) linked(k cidKey) int {
	for i := range cs.links {
		if cs.links[i].k == k {
			return i
		}
	}
	return -1
}

// take appends nd, and its links.
func (cs *carried) take(nd ipld.Node) error {
	cs.nodes = append(cs.nodes, nd)
	for _, l := range nd.Links() {
		if !isStateCid(l.Cid) {
			return errMalformedState
		}
		cs.links = append(cs.links, carriedLink{k: keyOf(l.Cid), c: l.Cid, parent: len(cs.nodes) - 1})
	}
	return nil
}

// seed begins f's first attempt with what its snapshot carries, staged as
// a fetched block is: what the roots do not reach becomes an intent of f
// before it is written, and what the blocks written link that none carried
// is discovered. A failure before the blocks are written ends the session,
// for the next attempt to begin again.
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

// stageCarried writes what the snapshot carries that the roots do not
// reach, and leaves in cs.cids what the blocks it wrote link.
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
		if err := store.dagService.AddMany(ctx, cs.written); err != nil {
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
