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

// Configures reports whether this repository is the project's CONFIGURATION —
// the one whose pushes are deploys rather than builds.
//
// IT ANSWERS A BOOL AND NOT THE PAIR, which is the same argument Repositories
// above makes from the other side: which repositories gate a project is auth's
// schema knowledge, and a caller handed both keys would be a caller deciding
// which of them counts. This one asks a question with one right answer.
//
// A repository the project does not name at all is `false` and not an error: a
// push can legitimately arrive for a repository registered to the project's
// code half, and that is a build rather than a failure.
func (c *ProjectClient) Configures(ctx context.Context, projectID, repositoryKey string) (bool, error) {
	if projectID == "" {
		return false, connect.NewError(connect.CodeInvalidArgument, errors.New("build: project id is required"))
	}
	if repositoryKey == "" {
		return false, connect.NewError(connect.CodeInvalidArgument, errors.New("build: repository key is required"))
	}

	var configures bool
	err := c.pool.Do(ctx, func(client *rpc.Client) error {
		projects := authv1connect.NewProjectServiceClient(client.HTTP(), client.URL())
		resp, err := projects.Get(ctx, connect.NewRequest(&authv1.ProjectRef{Id: projectID}))
		if err != nil {
			return err
		}
		configures = resp.Msg.GetConfigRepositoryKey() == repositoryKey
		return nil
	})
	if err != nil {
		return false, err
	}
	return configures, nil
}

// CodeRepository is the repository whose pushes build a project's code.
//
// IT IS ASKED RATHER THAN INFERRED because half of it is not in the config: a
// resource whose source is a bare path lives in the project's own code
// repository, and which repository that IS is auth's to say.
//
// A project with none registered answers with an empty string and no error.
// That is a real state — a project can be created with only a configuration
// repository — and it means no bare-path resource is built by any push, which
// is the right answer rather than an error on a push that was never going to
// build anything.
func (c *ProjectClient) CodeRepository(ctx context.Context, projectID string) (string, error) {
	if projectID == "" {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("build: project id is required"))
	}

	var key string
	err := c.pool.Do(ctx, func(client *rpc.Client) error {
		projects := authv1connect.NewProjectServiceClient(client.HTTP(), client.URL())
		resp, err := projects.Get(ctx, connect.NewRequest(&authv1.ProjectRef{Id: projectID}))
		if err != nil {
			return err
		}
		key = resp.Msg.GetCodeRepositoryKey()
		return nil
	})
	if err != nil {
		return "", err
	}
	return key, nil
}
