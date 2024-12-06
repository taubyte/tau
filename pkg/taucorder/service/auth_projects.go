package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
)

func (ps *projectsService) Get(ctx context.Context, req *connect.Request[pb.ByProjectRequest]) (*connect.Response[pb.Project], error) {
	ni, err := ps.getNode(req.Msg)
	if err != nil {
		return nil, err
	}

	pid := req.Msg.GetId()
	if pid == "" {
		return nil, errors.New("missing project")
	}

	prj := ni.authClient.Projects().Get(pid)
	if prj == nil {
		return nil, fmt.Errorf("can't fetch project `%s`", pid)
	}

	return connect.NewResponse(&pb.Project{
		Id:       prj.Id,
		Name:     prj.Name,
		Provider: prj.Provider,
		Repositories: &pb.ProjectRepos{
			Config: &pb.ProjectRepo{
				Id: &pb.RepositoryId{Id: &pb.RepositoryId_Github{
					Github: int64(prj.Git.Config.Id()),
				}},
				ProjectId: prj.Id,
			},
			Code: &pb.ProjectRepo{
				Id: &pb.RepositoryId{Id: &pb.RepositoryId_Github{
					Github: int64(prj.Git.Code.Id()),
				}},
				ProjectId: prj.Id,
			},
		},
	}), nil
}

func (ps *projectsService) List(ctx context.Context, req *connect.Request[pb.Node], stream *connect.ServerStream[pb.Project]) error {
	ni, err := ps.getNodeById(req.Msg.GetId())
	if err != nil {
		return err
	}

	prjs, err := ni.authClient.Projects().List()
	if err != nil {
		return fmt.Errorf("listing projects failed: %w", err)
	}

	for _, prj := range prjs {
		if err := stream.Send(&pb.Project{Id: prj}); err != nil {
			return err
		}
	}

	return nil
}
