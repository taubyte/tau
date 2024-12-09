package service

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/taubyte/tau/core/services/patrick"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
	"github.com/taubyte/tau/services/common"
)

func (ps *patrickService) Get(ctx context.Context, req *connect.Request[pb.GetJobRequest]) (*connect.Response[pb.Job], error) {
	ni, err := ps.getNode(req.Msg)
	if err != nil {
		return nil, err
	}

	jid := req.Msg.GetId()
	if jid == "" {
		return nil, errors.New("no job id")
	}

	job, err := ni.patrickClient.Get(jid)
	if err != nil {
		return nil, fmt.Errorf("get job failed: %w", err)
	}

	logs := make([]*pb.JobLog, 0, len(job.Logs))
	for _, l := range job.Logs {
		logs = append(logs, &pb.JobLog{Cid: l})
	}

	assets := make([]*pb.JobAsset, 0, len(job.AssetCid))
	for r, a := range job.AssetCid {
		assets = append(assets, &pb.JobAsset{RessourceId: r, Cid: a})
	}

	if job.Delay == nil {
		job.Delay = &patrick.DelayConfig{}
	}

	return connect.NewResponse(&pb.Job{
		Id:        job.Id,
		Timestamp: job.Timestamp,
		Status:    int32(job.Status),
		Logs:      logs,
		Meta: &pb.JobMeta{
			Ref:        job.Meta.Ref,
			Before:     job.Meta.Before,
			After:      job.Meta.After,
			HeadCommit: job.Meta.HeadCommit.ID,
			Repository: &pb.JobRepository{
				Id: &pb.RepositoryId{
					Id: &pb.RepositoryId_Github{
						Github: int64(job.Meta.Repository.ID),
					},
				},
				SshUrl: job.Meta.Repository.SSHURL,
				Branch: job.Meta.Repository.Branch,
			},
		},
		Assets:  assets,
		Attempt: int32(job.Attempt),
		Delay:   int64(job.Delay.Time) * 1000,
	}), nil

}

func (ps *patrickService) List(ctx context.Context, req *connect.Request[pb.Node], stream *connect.ServerStream[pb.Job]) error {
	ni, err := ps.getNodeById(req.Msg.GetId())
	if err != nil {
		return err
	}

	jids, err := ni.patrickClient.List()
	if err != nil {
		return fmt.Errorf("list jobs failed: %w", err)
	}

	for _, jid := range jids {
		err = stream.Send(&pb.Job{
			Id: jid,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (ps *patrickService) State(ctx context.Context, req *connect.Request[pb.ConsensusStateRequest]) (*connect.Response[pb.ConsensusState], error) {
	ni, err := ps.getNode(req.Msg)
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

	sts, err := ni.patrickClient.Peers(pid).DatabaseStats()
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

func (ps *patrickService) States(ctx context.Context, req *connect.Request[pb.Node], stream *connect.ServerStream[pb.ConsensusState]) error {
	ni, err := ps.getNodeById(req.Msg.GetId())
	if err != nil {
		return err
	}

	for _, pid := range ni.Peer().Peerstore().Peers() {
		protos, err := ni.Peer().Peerstore().GetProtocols(pid)
		if err == nil && slices.Contains(protos, protocol.ID(common.PatrickProtocol)) {
			sts, err := ni.patrickClient.Peers(pid).DatabaseStats()
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
