package clients

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/p2p/rpc"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// AuthProtocol is "/tau/auth/v2", derived from auth's schema exactly the way
// auth itself derives it (service-conventions.md §2d) rather than typed here.
// Any of auth's five generated service names gives the same answer, because
// the id names the proto package's listener.
var AuthProtocol = rpc.Protocol(authv2connect.RepositoryServiceName)

// RepositoryClient answers one question against tau.auth.v2's
// RepositoryService: which project a repository belongs to.
//
// ONE FACT, not the whole generated client. auth's Repository message also
// carries deploy_key, and naming that here would put private key material in
// the vocabulary of a package that has no use for it yet — the backend's
// Runner is the consumer that will, and widening this is that change
// (auth.proto's own comment leaves the question open "because nothing named
// the consumer").
type RepositoryClient struct {
	pool *Pool
}

// Repositories adapts a pool dialling auth. It refuses a pool built for
// another protocol: nothing else would notice, and the failure would surface
// as an Unimplemented from a service that simply does not have this method.
func Repositories(p *Pool) (*RepositoryClient, error) {
	if p == nil {
		return nil, errors.New("build: no pool")
	}
	if p.Protocol() != AuthProtocol {
		return nil, fmt.Errorf("build: repositories needs a pool on %s, got %s", AuthProtocol, p.Protocol())
	}
	return &RepositoryClient{pool: p}, nil
}

// Project returns the id of the project the repository under key belongs to.
//
// A repository this cloud has no registration for comes back as a
// connect.CodeNotFound error, unchanged from auth — the code is preserved
// rather than flattened into a sentinel so the caller can tell "this cloud
// does not know that repository" (a push to refuse) from "auth is unreachable"
// (a push to retry), which are the same string and very different answers.
func (c *RepositoryClient) Project(ctx context.Context, repositoryKey string) (string, error) {
	if repositoryKey == "" {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("build: repository key is required"))
	}

	var projectID string
	err := c.pool.Do(ctx, func(client *rpc.Client) error {
		repos := authv2connect.NewRepositoryServiceClient(client.HTTP(), client.URL())
		resp, err := repos.Get(ctx, connect.NewRequest(&authv2.RepositoryRef{RepositoryKey: repositoryKey}))
		if err != nil {
			return err
		}
		projectID = resp.Msg.GetProjectId()
		return nil
	})
	if err != nil {
		return "", err
	}
	if projectID == "" {
		return "", connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("build: repository %q is registered to no project", repositoryKey))
	}
	return projectID, nil
}

// DeployKey is the repository's own private key, for reading it.
//
// AUTH'S SCHEMA LEFT THIS OPEN "because nothing named the consumer", and the
// consumer is now named: services/build/deploy clones a repository at the
// commit a push named, and a private one needs a key to do it.
//
// PER REPOSITORY, WHICH IS THE POINT. One cloud serves repositories belonging
// to people who do not know each other; an account-scoped token leaked from one
// build would be a key to every repository in that account, and a deploy key is
// a key to one.
//
// An empty key with no error means auth has none for this repository, which is
// the right answer for a public one — the clone then proceeds unauthenticated
// and a private repository fails with git's own message rather than silently
// building an empty tree.
func (c *RepositoryClient) DeployKey(ctx context.Context, repositoryKey string) (string, error) {
	if repositoryKey == "" {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("build: repository key is required"))
	}

	var key string
	err := c.pool.Do(ctx, func(client *rpc.Client) error {
		repos := authv2connect.NewRepositoryServiceClient(client.HTTP(), client.URL())
		resp, err := repos.Get(ctx, connect.NewRequest(&authv2.RepositoryRef{RepositoryKey: repositoryKey}))
		if err != nil {
			return err
		}
		key = resp.Msg.GetDeployKey()
		return nil
	})
	if err != nil {
		return "", err
	}
	return key, nil
}
