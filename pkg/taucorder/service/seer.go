package service

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"connectrpc.com/connect"
	"github.com/taubyte/tau/core/services/seer"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
)

func (ss *seerService) List(ctx context.Context, req *connect.Request[pb.NodesListRequest], stream *connect.ServerStream[pb.Peer]) error {
	ni, err := ss.getNode(req.Msg)
	if err != nil {
		return err
	}

	var pids []string
	if service := req.Msg.GetService(); service != "" {
		pids, err = ni.seerClient.Usage().ListServiceId(service)
		if err != nil {
			return fmt.Errorf("fetching nodes for `%s` failed: %w", service, err)
		}
	} else {
		pids, err = ni.seerClient.Usage().List()
		if err != nil {
			return fmt.Errorf("fetching nodes failed: %w", err)
		}
	}

	for _, pid := range pids {
		err = stream.Send(&pb.Peer{Id: pid})
		if err != nil {
			return err
		}
	}

	return nil
}

func (ss *seerService) Location(ctx context.Context, req *connect.Request[pb.LocationRequest], stream *connect.ServerStream[pb.PeerLocation]) error {
	ni, err := ss.getNode(req.Msg)
	if err != nil {
		return err
	}

	var peers []*seer.Peer
	if reqPeers := req.Msg.GetPeers(); req.Msg.GetAll() || reqPeers != nil {
		allPeers, err := ni.seerClient.Geo().All()
		if err != nil {
			return fmt.Errorf("fetching location of all nodes failed: %w", err)
		}
		if reqPeers != nil {
			pids := reqPeers.GetPids()
			for _, p := range allPeers {
				if slices.Contains(pids, p.Id) {
					peers = append(peers, p)
				}
			}
		} else {
			peers = allPeers
		}
	} else {
		if req.Msg.GetArea() == nil || req.Msg.GetArea().GetLocation() == nil {
			return errors.New("area not provided")
		}

		peers, err = ni.seerClient.Geo().Distance(seer.Location{
			Latitude:  req.Msg.GetArea().GetLocation().GetLatitude(),
			Longitude: req.Msg.GetArea().GetLocation().GetLongitude(),
		}, req.Msg.GetArea().GetDistance())
		if err != nil {
			return fmt.Errorf("fetching nodes in area failed: %w", err)
		}
	}

	for _, p := range peers {
		err = stream.Send(&pb.PeerLocation{
			Peer: &pb.Peer{
				Id: p.Id,
			},
			Location: &pb.Location{
				Latitude:  p.Location.Location.Latitude,
				Longitude: p.Location.Location.Longitude,
			},
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (ss *seerService) Usage(ctx context.Context, req *connect.Request[pb.NodesUsageRequest]) (*connect.Response[pb.PeerUsage], error) {
	ni, err := ss.getNode(req.Msg)
	if err != nil {
		return nil, err
	}

	pid := req.Msg.GetPeer()
	if pid == "" {
		return nil, errors.New("peer id not provided")
	}

	usage, err := ni.seerClient.Usage().Get(pid)
	if err != nil {
		return nil, fmt.Errorf("fetching usage failed: %w", err)
	}

	return connect.NewResponse(&pb.PeerUsage{
		Peer: &pb.Peer{
			Id: pid,
		},
		Name:          usage.Name,
		Types:         usage.Type,
		Address:       usage.Address,
		Timestamp:     int64(usage.Timestamp),
		UsedMem:       int64(usage.UsedMem),
		TotalMem:      int64(usage.TotalMem),
		FreeMem:       int64(usage.FreeMem),
		TotalCpu:      int64(usage.TotalCpu),
		CpuCount:      int64(usage.CpuCount),
		CpuUser:       int64(usage.CpuUser),
		CpuNice:       int64(usage.CpuNice),
		CpuSystem:     int64(usage.CpuSystem),
		CpuIdle:       int64(usage.CpuIdle),
		CpuIowait:     int64(usage.CpuIowait),
		CpuIrq:        int64(usage.CpuIrq),
		CpuSoftirq:    int64(usage.CpuSoftirq),
		CpuSteal:      int64(usage.CpuSteal),
		CpuGuest:      int64(usage.CpuGuest),
		CpuGuestNice:  int64(usage.CpuGuestNice),
		CpuStatCount:  int64(usage.CpuStatCount),
		TotalDisk:     int64(usage.TotalDisk),
		FreeDisk:      int64(usage.FreeDisk),
		UsedDisk:      int64(usage.UsedDisk),
		AvailableDisk: int64(usage.AvailableDisk),
	}), nil
}
