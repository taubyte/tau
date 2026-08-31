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
		// Id is left unset: the repository is named by its clone URL and
		// RepositoryId can only carry a numeric github id, so there is nothing
		// truthful to put here until the message changes. An absent field is a
		// value the reader already handles; a fabricated number is not.
		Repositories: &pb.ProjectRepos{
			Config: &pb.ProjectRepo{ProjectId: prj.Id},
			Code:   &pb.ProjectRepo{ProjectId: prj.Id},
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
