package clients

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/p2p/rpc"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// RegistrarClient is the two writes the public frontend forwards to auth when a
// developer links a project: register a repository, and create the project over
// two of them. Both are auth's to perform; the frontend's part is deciding the
// caller may, which it does before calling either.
type RegistrarClient struct {
	pool *Pool
}

// Registrar adapts a pool dialling auth.
func Registrar(p *Pool) (*RegistrarClient, error) {
	if err := requireProtocol(p, AuthProtocol, "registrar"); err != nil {
		return nil, err
	}
	return &RegistrarClient{pool: p}, nil
}

// Register registers a repository with auth, which mints its deploy key. A
// repository the same caller already registered is not an error (auth's rule),
// so a retried link converges.
func (c *RegistrarClient) Register(ctx context.Context, repositoryKey string) error {
	if repositoryKey == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("build: repository key is required"))
	}
	return c.pool.Do(ctx, func(client *rpc.Client) error {
		repos := authv2connect.NewRepositoryServiceClient(client.HTTP(), client.URL())
		_, err := repos.Register(ctx, connect.NewRequest(&authv2.RepositoryRef{RepositoryKey: repositoryKey}))
		return err
	})
}

// Create creates a project over a configuration and a code repository and
// returns the id auth gave it.
func (c *RegistrarClient) Create(ctx context.Context, name, configKey, codeKey string) (string, error) {
	var projectID string
	err := c.pool.Do(ctx, func(client *rpc.Client) error {
		projects := authv2connect.NewProjectServiceClient(client.HTTP(), client.URL())
		resp, err := projects.Create(ctx, connect.NewRequest(&authv2.CreateProjectRequest{
			Name:                name,
			ConfigRepositoryKey: configKey,
			CodeRepositoryKey:   codeKey,
		}))
		if err != nil {
			return err
		}
		projectID = resp.Msg.GetId()
		return nil
	})
	return projectID, err
}
