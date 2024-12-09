package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"connectrpc.com/connect"
	"github.com/taubyte/tau/core/services/auth"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
)

func (ps *reposService) Get(ctx context.Context, req *connect.Request[pb.ByRepositoryRequest]) (*connect.Response[pb.ProjectRepo], error) {
	ni, err := ps.getNode(req.Msg)
	if err != nil {
		return nil, err
	}

	var repo auth.GithubRepository
	switch rid := req.Msg.Id.Id.(type) {
	case *pb.RepositoryId_Github:
		repo, err = ni.authClient.Repositories().Github().Get(int(rid.Github))
	default:
		return nil, errors.New("git provider not supported")
	}

	if err != nil {
		return nil, fmt.Errorf("fetching repository failed: %w", err)
	}

	return connect.NewResponse(&pb.ProjectRepo{
		Id:               req.Msg.Id,
		ProjectId:        repo.Project(),
		DeployKeyPrivate: repo.PrivateKey(),
	}), nil

}

func (ps *reposService) List(ctx context.Context, req *connect.Request[pb.Node], stream *connect.ServerStream[pb.ProjectRepo]) error {
	ni, err := ps.getNodeById(req.Msg.GetId())
	if err != nil {
		return err
	}

	repos, err := ni.authClient.Repositories().Github().List()
	if err != nil {
		return fmt.Errorf("listing repos failed: %w", err)
	}

	for _, repo := range repos {
		repoId, err := strconv.ParseInt(repo, 10, 64)
		if err == nil {
			err = stream.Send(&pb.ProjectRepo{
				Id: &pb.RepositoryId{Id: &pb.RepositoryId_Github{
					Github: repoId,
				}},
			})
			if err != nil {
				return err
			}
		}
	}

	return nil
}
