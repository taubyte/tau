package clients

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/p2p/rpc"
	authv1 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1/authv1connect"
)

// ProjectClient answers what gates a project, against tau.auth.v1's
// ProjectService.
type ProjectClient struct {
	pool *Pool
}

// Projects adapts a pool dialling auth.
func Projects(p *Pool) (*ProjectClient, error) {
	if err := requireProtocol(p, AuthProtocol, "projects"); err != nil {
		return nil, err
	}
	return &ProjectClient{pool: p}, nil
}

// Repositories returns the repository keys that gate a project: writing to any
// one of them is writing to the project.
//
// ONE SLICE, NOT TWO STRINGS. auth's Project carries config_repository_key and
// code_repository_key, and the caller checks both the same way — so returning
// them as a pair would be two same-typed returns nothing distinguishes, which
// is a transposition waiting to happen and a decision ("which of the two
// counts") the caller does not get to make. Which repositories gate a project
// is auth's schema knowledge; that it is currently exactly two of them is not
// something the frontend should be able to observe.
//
// A key auth left empty is dropped rather than returned as "": an empty key
// matches no repository, and a caller that saw one would have to know that.
func (c *ProjectClient) Repositories(ctx context.Context, projectID string) ([]string, error) {
	if projectID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("build: project id is required"))
	}

	var keys []string
	err := c.pool.Do(ctx, func(client *rpc.Client) error {
		projects := authv1connect.NewProjectServiceClient(client.HTTP(), client.URL())
		resp, err := projects.Get(ctx, connect.NewRequest(&authv1.ProjectRef{Id: projectID}))
		if err != nil {
			return err
		}
		keys = keys[:0]
		for _, key := range []string{resp.Msg.GetConfigRepositoryKey(), resp.Msg.GetCodeRepositoryKey()} {
			if key != "" {
				keys = append(keys, key)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		// A project with neither repository is one nobody can be shown to own,
		// so it is refused here rather than becoming an empty allow-list the
		// caller has to remember denies.
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("build: project %q has no repositories", projectID))
	}
	return keys, nil
}
