package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
)

func (ps *reposService) Get(ctx context.Context, req *connect.Request[pb.ByRepositoryRequest]) (*connect.Response[pb.ProjectRepo], error) {
	if _, err := ps.getNode(req.Msg); err != nil {
		return nil, err
	}

	// RepositoryId still carries `int64 github`, and a repository is now named
	// by its clone URL. There is no honest conversion between the two: a decimal
	// id is not a key, and inventing one would file or fetch under a name no
	// clone produces. It says so until the message carries the key.
	switch req.Msg.Id.Id.(type) {
	case *pb.RepositoryId_Github:
		return nil, errors.New("a repository is named by its clone URL; RepositoryId still carries a numeric github id and cannot address one")
	default:
		return nil, errors.New("git provider not supported")
	}
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

	// The `if err == nil` that used to wrap this send DROPPED every entry that
	// did not parse as a decimal, silently — a listing that could not be
	// expressed came back as an empty listing rather than as a failure, and an
	// empty list of repositories reads as "there are none". Every key is now a
	// clone-URL key, so that path would drop all of them. It reports instead.
	if len(repos) > 0 {
		return fmt.Errorf("%d repositories are named by clone URL; RepositoryId still carries a numeric github id and cannot express them", len(repos))
	}

	return nil
}
