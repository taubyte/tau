package kvdb

import (
	"context"
	"errors"

	dag "github.com/ipfs/boxo/ipld/merkledag"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"google.golang.org/protobuf/proto"
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
	delta := &pb.Delta{}
	if err := proto.Unmarshal(protonode.Data(), delta); err != nil {
		return nil, err
	}
	return delta, nil
}

// makeNode makes the block for delta, linking the given heads.
func makeNode(delta *pb.Delta, heads []Head) (ipld.Node, error) {
	data, err := proto.Marshal(delta)
	if err != nil {
		return nil, err
	}
	nd := dag.NodeWithData(data)
	for _, h := range heads {
		if err := nd.AddRawLink("", &ipld.Link{Cid: h.Cid}); err != nil {
			return nil, err
		}
	}
	// Ensure we work with CIDv1
	err = nd.SetCidBuilder(dag.V1CidPrefix())
	return nd, err
}
