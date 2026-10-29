package kvdb

import (
	"context"
	"errors"

	dag "github.com/ipfs/boxo/ipld/merkledag"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
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
	protonode, ok := nd.(*dag.ProtoNode)
	if !ok {
		return nil, errors.New("node is not a ProtoNode")
	}
	// The delta's bytes and strings alias the block's data, which is never
	// written to once made, for as long as the delta is held.
	delta := &pb.Delta{}
	if err := delta.UnmarshalVTUnsafe(protonode.Data()); err != nil {
		return nil, err
	}
	return delta, nil
}

// makeNode makes the block for delta, linking the given heads.
func makeNode(delta *pb.Delta, heads []Head) (ipld.Node, error) {
	data, err := delta.MarshalVT()
	if err != nil {
		return nil, err
	}
	nd := dag.NodeWithData(data)
	if err := setLinks(nd, len(heads), func(i int) cid.Cid { return heads[i].Cid }); err != nil {
		return nil, err
	}
	return nd, nd.SetCidBuilder(v1Prefix)
}

// v1Prefix is the builder of every block's cid, made once: as an interface
// it would otherwise be allocated per block.
var v1Prefix cid.Builder = dag.V1CidPrefix()

// setLinks gives nd the n links at(0) .. at(n-1) with one allocation for
// them: SetLinks keeps the pointers and copies only the slice of them.
func setLinks(nd *dag.ProtoNode, n int, at func(int) cid.Cid) error {
	if n == 0 {
		return nil
	}
	links := make([]ipld.Link, n)
	var buf [32]*ipld.Link
	ptrs := buf[:0]
	for i := range links {
		links[i].Cid = at(i)
		ptrs = append(ptrs, &links[i])
	}
	return nd.SetLinks(ptrs)
}
