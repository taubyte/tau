package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
)

func (ps *hooksService) Get(ctx context.Context, req *connect.Request[pb.ByHookRequest]) (*connect.Response[pb.Hook], error) {
	ni, err := ps.getNode(req.Msg)
	if err != nil {
		return nil, err
	}

	hid := req.Msg.GetId()
	if hid == "" {
		return nil, errors.New("empty id")
	}

	hook, err := ni.authClient.Hooks().Get(hid)
	if err != nil {
		return nil, fmt.Errorf("fetching hook failed: %w", err)
	}

	ghHook, err := hook.Github()
	if err != nil {
		return nil, errors.New("not a github hook")
	}

	return connect.NewResponse(&pb.Hook{
		Id: ghHook.Id,
		Provider: &pb.Hook_Github{
			Github: &pb.GithubHook{
				Id:           ghHook.Id,
				RepositoryId: int64(ghHook.GithubId),
				Secret:       ghHook.Secret,
			},
		},
	}), nil
}

func (ps *hooksService) List(ctx context.Context, req *connect.Request[pb.Node], stream *connect.ServerStream[pb.Hook]) error {
	ni, err := ps.getNodeById(req.Msg.GetId())
	if err != nil {
		return err
	}

	hooks, err := ni.authClient.Hooks().List()
	if err != nil {
		return fmt.Errorf("listing repos failed: %w", err)
	}

	for _, hookId := range hooks {
		if err := stream.Send(&pb.Hook{Id: hookId}); err != nil {
			return err
		}
	}

	return nil
}
