package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
	"github.com/taubyte/tau/services/common"
)

func (as *authService) Discover(ctx context.Context, req *connect.Request[pb.DiscoverServiceRequest], stream *connect.ServerStream[pb.Peer]) error {
	ni, err := as.getNode(req.Msg)
	if err != nil {
		return err
	}

	to := time.Duration(req.Msg.GetTimeout())
	if to <= 0 {
		to = DefaultDiscoverDuration
	}

	count := req.Msg.GetCount()
	if count <= 0 {
		count = 100
	}

	dCtx, dCtxC := context.WithTimeout(ctx, to)
	defer dCtxC()

	peers, err := ni.Discovery().FindPeers(dCtx, common.AuthProtocol, discovery.Limit(int(count)))
	if err != nil {
		return fmt.Errorf("failed to discover `%s`: %w", common.AuthProtocol, err)
	}

	for p := range peers {
		addrs := make([]string, 0, len(p.Addrs))
		for _, addr := range p.Addrs {
			addrs = append(addrs, addr.String())
		}
		err = stream.Send(&pb.Peer{
			Id:        p.ID.String(),
			Addresses: addrs,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (as *authService) List(ctx context.Context, req *connect.Request[pb.Node], stream *connect.ServerStream[pb.Peer]) error {
	ni, err := as.getNodeById(req.Msg.GetId())
	if err != nil {
		return err
	}

	for _, p := range ni.Peer().Peerstore().Peers() {
		protos, err := ni.Peer().Peerstore().GetProtocols(p)
		if err == nil && slices.Contains(protos, protocol.ID(common.AuthProtocol)) {
			pinfo := ni.Peer().Peerstore().PeerInfo(p)
			addrs := make([]string, 0, len(pinfo.Addrs))
			for _, addr := range pinfo.Addrs {
				addrs = append(addrs, addr.String())
			}
			err = stream.Send(&pb.Peer{
				Id:        p.String(),
				Addresses: addrs,
			})
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (as *authService) State(ctx context.Context, req *connect.Request[pb.ConsensusStateRequest]) (*connect.Response[pb.ConsensusState], error) {
	ni, err := as.getNode(req.Msg)
	if err != nil {
		return nil, err
	}

	spid := req.Msg.GetPid()
	if spid == "" {
		return nil, errors.New("empty peer id")
	}

	pid, err := peer.Decode(spid)
	if err != nil {
		return nil, fmt.Errorf("decoding peer id: %w", err)
	}

	sts, err := ni.authClient.Peers(pid).Stats().Database()
	if err != nil {
		return nil, fmt.Errorf("fetching database state: %w", err)
	}

	heads := make([]string, 0, len(sts.Heads()))
	for _, cid := range sts.Heads() {
		heads = append(heads, cid.String())
	}

	return connect.NewResponse(&pb.ConsensusState{
		Member: &pb.Peer{
			Id: spid,
		},
		Consensus: &pb.ConsensusState_Crdt{
			Crdt: &pb.CRDTState{
				Heads: heads,
			},
		},
	}), nil
}

func (as *authService) States(ctx context.Context, req *connect.Request[pb.Node], stream *connect.ServerStream[pb.ConsensusState]) error {
	ni, err := as.getNodeById(req.Msg.GetId())
	if err != nil {
		return err
	}

	for _, pid := range ni.Peer().Peerstore().Peers() {
		protos, err := ni.Peer().Peerstore().GetProtocols(pid)
		if err == nil && slices.Contains(protos, protocol.ID(common.AuthProtocol)) {
			sts, err := ni.authClient.Peers(pid).Stats().Database()
			if err == nil {
				heads := make([]string, 0, len(sts.Heads()))
				for _, cid := range sts.Heads() {
					heads = append(heads, cid.String())
				}

				stream.Send(&pb.ConsensusState{
					Member: &pb.Peer{
						Id: pid.String(),
					},
					Consensus: &pb.ConsensusState_Crdt{
						Crdt: &pb.CRDTState{
							Heads: heads,
						},
					},
				})
			}
		}
	}

	return nil
}
