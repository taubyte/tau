package service

import (
	"context"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
	slices "github.com/taubyte/utils/slices/string"
)

func (hs *hoarderService) List(ctx context.Context, req *connect.Request[pb.Node], stream *connect.ServerStream[pb.StashedItem]) error {
	ni, err := hs.getNodeById(req.Msg.GetId())
	if err != nil {
		return err
	}

	cids, err := ni.hoarderClient.List()
	if err != nil {
		return err
	}

	for _, cid := range cids {
		err = stream.Send(&pb.StashedItem{
			Cid: cid,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (hs *hoarderService) Stash(ctx context.Context, req *connect.Request[pb.StashRequest]) (*connect.Response[pb.Empty], error) {
	ni, err := hs.getNode(req.Msg)
	if err != nil {
		return nil, err
	}

	pbpeers := req.Msg.GetProviders()
	peers := make([]string, 0)
	for _, p := range pbpeers {
		spid := p.GetId()
		maddrs := make([]multiaddr.Multiaddr, 0, len(p.Addresses))
		for _, a := range p.Addresses {
			ma, err := multiaddr.NewMultiaddr(a)
			if err != nil {
				return nil, err
			}
			maonly, mapid := peer.SplitAddr(ma) // strip /p2p/ID
			if spid == "" {                     // if pid not provided, get it from MA
				spid = mapid.String()
			}
			maddrs = append(maddrs, maonly)
		}

		// validate pid
		_, err := peer.Decode(spid)
		if err != nil {
			return nil, err
		}

		// add all addrs
		p2ppart, _ := multiaddr.NewComponent("p2p", spid)
		for _, ma := range maddrs {
			peers = append(peers, ma.Encapsulate(p2ppart).String())
		}
	}

	_, err = ni.hoarderClient.Stash(req.Msg.GetCid(), slices.Unique(peers)...)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.Empty{}), nil
}
