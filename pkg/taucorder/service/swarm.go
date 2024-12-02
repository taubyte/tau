package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/discovery"
	"github.com/multiformats/go-multiaddr"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"

	"github.com/libp2p/go-libp2p/core/peer"
)

var (
	MaxPingConcurrency      = 4
	DefaultPingCount        = 3
	DefaultPingDuration     = 10 * time.Second
	DefaultConnectTimeout   = 5 * time.Second
	DefaultDiscoverDuration = 10 * time.Second
)

func (ss *swarmService) Connect(ctx context.Context, req *connect.Request[pb.ConnectRequest]) (*connect.Response[pb.Peer], error) {
	ni, err := ss.getNode(req.Msg)
	if err != nil {
		return nil, err
	}

	ma, err := multiaddr.NewMultiaddr(req.Msg.GetAddress())
	if err != nil {
		return nil, fmt.Errorf("failed to parse peer address: %w", err)
	}

	addrInfo, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		return nil, fmt.Errorf("failed to convert multiaddr: %w", err)
	}

	to := time.Duration(req.Msg.GetTimeout())
	if to <= 0 {
		to = DefaultConnectTimeout
	}

	cCtx, cCtxC := context.WithTimeout(ctx, to)
	defer cCtxC()

	err = ni.Peer().Connect(cCtx, *addrInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to peer: %w", err)
	}

	ni.Peering().AddPeer(*addrInfo)

	maddrs := ni.Peer().Peerstore().Addrs(addrInfo.ID)
	addrs := make([]string, 0, len(maddrs))
	for _, addr := range ni.Peer().Peerstore().Addrs(addrInfo.ID) {
		addrs = append(addrs, addr.String())
	}

	return connect.NewResponse(&pb.Peer{
		Id:        addrInfo.ID.String(),
		Addresses: addrs,
	}), nil
}

func (ss *swarmService) Discover(ctx context.Context, req *connect.Request[pb.DiscoverRequest], stream *connect.ServerStream[pb.Peer]) error {
	ni, err := ss.getNode(req.Msg)
	if err != nil {
		return err
	}

	to := time.Duration(req.Msg.GetTimeout())
	if to <= 0 {
		to = DefaultDiscoverDuration
	}

	service := req.Msg.GetService()
	if service == "" {
		return errors.New("service is empty")
	}

	count := req.Msg.GetCount()
	if count <= 0 {
		count = 100
	}

	dCtx, dCtxC := context.WithTimeout(ctx, to)
	defer dCtxC()

	peers, err := ni.Discovery().FindPeers(dCtx, service, discovery.Limit(int(count)))
	if err != nil {
		return fmt.Errorf("failed to discover `%s`: %w", service, err)
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

func (ss *swarmService) List(ctx context.Context, req *connect.Request[pb.ListRequest], stream *connect.ServerStream[pb.Peer]) error {
	ni, err := ss.getNode(req.Msg)
	if err != nil {
		return err
	}

	peerIDs := ni.Peer().Peerstore().Peers()

	var (
		pingCount   int
		concurrency int
	)

	pingParams := req.Msg.GetPing()

	if pingParams != nil {
		pingCount = int(pingParams.GetCount())
		if pingCount < 1 {
			pingCount = DefaultPingCount
		}

		concurrency = int(pingParams.GetConcurrency())

		peerCount := len(peerIDs)

		if concurrency > peerCount {
			concurrency = peerCount
		}
	}

	if concurrency < 1 || concurrency > MaxPingConcurrency {
		concurrency = MaxPingConcurrency
	}

	peerChan := make(chan peer.ID, len(peerIDs))
	resultChan := make(chan *pb.Peer, len(peerIDs))
	errChan := make(chan error, concurrency)

	for _, pid := range peerIDs {
		peerChan <- pid
	}
	close(peerChan)

	pCtx, pCtxC := context.WithCancel(ctx)
	defer pCtxC()

	// make sure we stop the ping if service is shutdown
	go func() {
		select {
		case <-ss.ctx.Done():
			pCtxC()
		case <-pCtx.Done():
		}
	}()

	worker := func() {
		for pid := range peerChan {
			var pstatus *pb.PingStatus
			if pingParams != nil {
				count, latency, pingErr := ni.Ping(pCtx, pid.String(), pingCount)
				pstatus = &pb.PingStatus{
					Up:         pingErr == nil,
					Count:      int32(count),
					CountTotal: int32(pingCount),
					Latency:    int64(latency),
				}
			}

			peerInfo := ni.Peer().Peerstore().PeerInfo(pid)
			maddrs, err := peer.AddrInfoToP2pAddrs(&peerInfo)
			if err != nil {
				errChan <- fmt.Errorf("converting peer info: %w", err)
				return
			}

			addrs := make([]string, 0, len(maddrs))
			for _, addr := range maddrs {
				addrs = append(addrs, addr.String())
			}

			resultChan <- &pb.Peer{
				Id:         pid.String(),
				Addresses:  addrs,
				PingStatus: pstatus,
			}
		}
	}

	for i := 0; i < concurrency; i++ {
		go worker()
	}

	go func() {
		for i := 0; i < len(peerIDs); i++ {
			select {
			case peer := <-resultChan:
				if err := stream.Send(peer); err != nil {
					errChan <- err
					return
				}
			case <-ctx.Done():
				errChan <- ctx.Err()
				return
			}
		}
		close(errChan)
	}()

	for err := range errChan {
		if err != nil {
			return err
		}
	}

	return nil
}

func (ss *swarmService) Ping(ctx context.Context, req *connect.Request[pb.PingRequest]) (*connect.Response[pb.Peer], error) {
	ni, err := ss.getNode(req.Msg)
	if err != nil {
		return nil, err
	}

	to := time.Duration(req.Msg.GetTimeout())
	if to == 0 {
		to = DefaultPingDuration
	}

	count := int(req.Msg.GetCount())
	if count == 0 {
		count = DefaultPingCount
	}

	pid := req.Msg.GetPid()
	if pid == "" {
		return nil, errors.New("empty pid")
	}

	pCtx, pCtxC := context.WithTimeout(ctx, to)
	defer pCtxC()

	// make sure we stop the ping if service is shutdown
	go func() {
		select {
		case <-ss.ctx.Done():
			pCtxC()
		case <-pCtx.Done():
		}
	}()

	okCount, latency, err := ni.Ping(pCtx, pid, count)

	return connect.NewResponse(&pb.Peer{Id: pid, PingStatus: &pb.PingStatus{
		Up:         err == nil,
		Count:      int32(okCount),
		CountTotal: int32(count),
		Latency:    int64(latency),
	}}), nil
}

func (ss *swarmService) Wait(ctx context.Context, req *connect.Request[pb.WaitRequest]) (*connect.Response[pb.Empty], error) {
	ni, err := ss.getNode(req.Msg)
	if err != nil {
		return nil, err
	}

	err = ni.WaitForSwarm(time.Duration(req.Msg.GetTimeout()))
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.Empty{}), nil
}
