package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
)

func (ms *monkeyService) Get(ctx context.Context, req *connect.Request[pb.GetJobInstanceRequest]) (*connect.Response[pb.Job], error) {
	ni, err := ms.getNode(req.Msg)
	if err != nil {
		return nil, err
	}

	jid := req.Msg.GetId()
	if jid == "" {
		return nil, errors.New("no job id")
	}

	jstatus, err := ni.monkeyClient.Status(jid)
	if err != nil {
		return nil, fmt.Errorf("fetching job failed: %w", err)
	}

	return connect.NewResponse(&pb.Job{
		Id:     jid,
		Status: int32(jstatus.Status),
		Logs: []*pb.JobLog{{
			Cid: jstatus.Logs,
		}}}), nil
}

func (ms *monkeyService) List(ctx context.Context, req *connect.Request[pb.Node], stream *connect.ServerStream[pb.Job]) error {
	ni, err := ms.getNodeById(req.Msg.GetId())
	if err != nil {
		return err
	}

	jids, err := ni.monkeyClient.List()
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
