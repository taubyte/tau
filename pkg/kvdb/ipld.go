package kvdb

import (
	"context"
	"errors"

	dag "github.com/ipfs/boxo/ipld/merkledag"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
)

// IPLD related things

var _ ipld.NodeGetter = (*crdtNodeGetter)(nil)

// crdtNodeGetter wraps an ipld.NodeGetter with some additional utility methods
type crdtNodeGetter struct {
	ipld.NodeGetter
}

func (ng *crdtNodeGetter) GetDelta(ctx context.Context, c cid.Cid) (ipld.Node, []byte, error) {
	nd, err := ng.Get(ctx, c)
	if err != nil {
		return nil, nil, err
	}
	delta, err := extractDelta(nd)
	return nd, delta, err
}

type deltaOption struct {
	delta []byte
	node  ipld.Node
	err   error
}

// GetDeltas uses GetMany to obtain many deltas.
func (ng *crdtNodeGetter) GetDeltas(ctx context.Context, cids []cid.Cid) <-chan *deltaOption {
	deltaOpts := make(chan *deltaOption, 1)
	go func() {
		defer close(deltaOpts)
		nodeOpts := ng.GetMany(ctx, cids)
		for nodeOpt := range nodeOpts {
			if nodeOpt.Err != nil {
				deltaOpts <- &deltaOption{err: nodeOpt.Err}
				continue
			}
			delta, err := extractDelta(nodeOpt.Node)
			if err != nil {
				deltaOpts <- &deltaOption{err: err}
				continue
			}
			deltaOpts <- &deltaOption{
				delta: delta,
				node:  nodeOpt.Node,
			}
		}
	}()
	return deltaOpts
}

func extractDelta(nd ipld.Node) ([]byte, error) {
	protonode, ok := nd.(*dag.ProtoNode)
	if !ok {
		return nil, errors.New("node is not a ProtoNode")
	}
	return protonode.Data(), nil
}

func makeNode(delta Delta, heads []Head) (ipld.Node, error) {
	data, err := delta.Marshal()
	if err != nil {
		return nil, err
	}

	nd := dag.NodeWithData(data)
	for _, h := range heads {
		err = nd.AddRawLink("", &ipld.Link{Cid: h.Cid})
		if err != nil {
			return nil, err
		}
	}
	// Ensure we work with CIDv1
	err = nd.SetCidBuilder(dag.V1CidPrefix())
	return nd, err
}
