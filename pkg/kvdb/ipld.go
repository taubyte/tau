package kvdb

import (
	"context"

	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"github.com/taubyte/tau/pkg/kvdb/statetree"
)

type crdtNodeGetter struct {
	ipld.NodeGetter
}

func (ng *crdtNodeGetter) GetDelta(ctx context.Context, c cid.Cid) (ipld.Node, *pb.Delta, error) {
	nd, err := ng.Get(ctx, c)
	if err != nil {
		return nil, nil, err
	}
	delta, err := extractDelta(nd)
	return nd, delta, err
}

type deltaOption struct {
	delta *pb.Delta
	node  ipld.Node
	err   error
}

// The channel holds a delta for every cid, so a delta fetched in time is kept
// however long the caller takes to read it.
func (ng *crdtNodeGetter) GetDeltas(ctx context.Context, cids []cid.Cid) <-chan *deltaOption {
	deltaOpts := make(chan *deltaOption, len(cids))
	go func() {
		defer close(deltaOpts)
		for nodeOpt := range ng.GetMany(ctx, cids) {
			opt := &deltaOption{err: nodeOpt.Err, node: nodeOpt.Node}
			if opt.err == nil {
				opt.delta, opt.err = extractDelta(nodeOpt.Node)
			}
			deltaOpts <- opt
		}
	}()
	return deltaOpts
}

func extractDelta(nd ipld.Node) (*pb.Delta, error) {
	data, err := statetree.BlockData(nd)
	if err != nil {
		return nil, err
	}
	// The delta's bytes and strings alias the block's data, which is never
	// written to once made, for as long as the delta is held.
	delta := &pb.Delta{}
	if err := delta.UnmarshalVTUnsafe(data); err != nil {
		return nil, err
	}
	return delta, nil
}

func makeNode(delta *pb.Delta, heads []Head) (ipld.Node, error) {
	msg := pb.DeltaBlockFromVTPool()
	defer func() {
		// The delta is the caller's: the pool must not take it back too.
		msg.Data = nil
		clear(msg.Links)
		msg.ReturnToVTPool()
	}()
	for _, h := range heads {
		msg.Links = append(msg.Links, bytesOf(h.Cid.KeyString()))
	}
	msg.Data = delta
	raw := statetree.BlockBuffer(msg.SizeVT())
	if _, err := msg.MarshalToSizedBufferVT(raw); err != nil {
		return nil, err
	}
	return statetree.BlockInBuffer(raw)
}

func readBlock(ctx context.Context, dags ipld.NodeGetter, c cid.Cid) (blocks.Block, error) {
	if r, ok := dags.(statetree.BlockReader); ok {
		return r.ReadBlock(ctx, c)
	}
	return dags.Get(ctx, c)
}

func blockLinks(nd ipld.Node, into []cid.Cid) ([]cid.Cid, error) {
	_, links, err := statetree.DecodeBlock(nd, into)
	return links, err
}

const heldLimit = 1 << 12

// A heldDAG keeps, up to heldLimit, the blocks a snapshot merge's fetch
// received and those it read, so each is decoded once per merge. One goroutine
// uses it at a time.
type heldDAG struct {
	ipld.DAGService
	nodes map[cid.Cid]ipld.Node
}

func newHeldDAG(dags ipld.DAGService) *heldDAG {
	return &heldDAG{DAGService: dags, nodes: map[cid.Cid]ipld.Node{}}
}

func (h *heldDAG) keep(nd ipld.Node) {
	if h != nil && len(h.nodes) < heldLimit {
		h.nodes[nd.Cid()] = nd
	}
}

func (h *heldDAG) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	if nd, ok := h.nodes[c]; ok {
		return nd, nil
	}
	nd, err := h.DAGService.Get(ctx, c)
	if err != nil {
		return nil, err
	}
	h.keep(nd)
	return nd, nil
}

func (h *heldDAG) Remove(ctx context.Context, c cid.Cid) error {
	delete(h.nodes, c)
	return h.DAGService.Remove(ctx, c)
}

func (h *heldDAG) RemoveMany(ctx context.Context, cids []cid.Cid) error {
	for _, c := range cids {
		delete(h.nodes, c)
	}
	return h.DAGService.RemoveMany(ctx, cids)
}
