package kvdb

import (
	"context"

	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	"github.com/taubyte/tau/pkg/kvdb/mst"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// crdtNodeGetter wraps an ipld.NodeGetter with methods that decode the
// delta a node carries.
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

// GetDeltas uses GetMany to obtain many deltas.
//
// A DELTA FETCHED IN TIME IS KEPT, however long the caller takes to read it.
// The channel held one delta, and once ctx expired the next was dropped while
// the caller was still queueing the one before for a worker: a walk whose
// workers waited on store.mu lost blocks it had fetched, recorded them as
// missing, and left them to the next repair, a minute later. The channel now
// holds a delta for every cid, so handing one over never waits on the caller.
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
	data, err := mst.BlockData(nd)
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

// makeNode makes the block for delta, linking the given heads, in one
// marshal of a DeltaBlock.
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
	raw := mst.BlockBuffer(msg.SizeVT())
	if _, err := msg.MarshalToSizedBufferVT(raw); err != nil {
		return nil, err
	}
	return mst.BlockInBuffer(raw)
}

// readBlock is the block c names, read through dags' BlockReader when it is
// one, else decoded by its Get.
func readBlock(ctx context.Context, dags ipld.NodeGetter, c cid.Cid) (blocks.Block, error) {
	if r, ok := dags.(mst.BlockReader); ok {
		return r.ReadBlock(ctx, c)
	}
	return dags.Get(ctx, c)
}

// blockLinks appends the links of nd, one of kvdb's blocks, to into.
func blockLinks(nd ipld.Node, into []cid.Cid) ([]cid.Cid, error) {
	_, links, err := mst.DecodeBlock(nd, into)
	return links, err
}

// heldLimit bounds how many blocks a heldDAG keeps.
const heldLimit = 1 << 12

// A heldDAG is the DAG one snapshot merge reads: it keeps, up to heldLimit,
// the blocks its state fetch received and those the merge has read, so that
// a block is decoded once per merge rather than once per tree that reads
// it. It is used by one goroutine at a time and dropped with the merge.
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
