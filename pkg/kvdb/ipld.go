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
func (ng *crdtNodeGetter) GetDeltas(ctx context.Context, cids []cid.Cid) <-chan *deltaOption {
	deltaOpts := make(chan *deltaOption, 1)
	go func() {
		defer close(deltaOpts)
		for nodeOpt := range ng.GetMany(ctx, cids) {
			opt := &deltaOption{err: nodeOpt.Err, node: nodeOpt.Node}
			if opt.err == nil {
				opt.delta, opt.err = extractDelta(nodeOpt.Node)
			}
			select {
			case deltaOpts <- opt:
			case <-ctx.Done():
				return
			}
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
