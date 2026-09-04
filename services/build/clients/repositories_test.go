package clients

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/discovery"
	authv1 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1"
	"github.com/taubyte/tau/services/build/backend"
	"github.com/taubyte/tau/services/build/deploy"
)

// The interface satisfaction lives HERE rather than in repositories.go: a test
// file may import the role package, and production code in this package may
// not (services/build/boundary_test.go). This is the whole of what that
// separation costs.
var _ backend.Repositories = (*RepositoryClient)(nil)

func TestAuthProtocolIsWalkedOffAuthsSchema(t *testing.T) {
	require.EqualValues(t, "/tau/auth/v1", AuthProtocol)
}

func TestRepositoriesRefusesAPoolForTheWrongProtocol(t *testing.T) {
	mn := newMocknet(t)

	c, err := Repositories(nil)
	require.Error(t, err)
	require.Nil(t, c)

	p, err := NewPool(newHost(t, mn), discovery.Static(), "/tau/build/v1", 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	c, err = Repositories(p)
	require.ErrorContains(t, err, "/tau/auth/v1")
	require.Nil(t, c)
}

func newRepositories(t *testing.T, get func(string) (*authv1.Repository, error)) *RepositoryClient {
	t.Helper()
	mn := newMocknet(t)
	caller := newHost(t, mn)
	server := serveAuth(t, mn, get)

	p, err := NewPool(caller, discovery.Static(server.ID()), AuthProtocol, 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	c, err := Repositories(p)
	require.NoError(t, err)
	return c
}

func TestProjectReadsTheProjectIDOffAuth(t *testing.T) {
	var asked string
	c := newRepositories(t, func(key string) (*authv1.Repository, error) {
		asked = key
		return &authv1.Repository{RepositoryKey: key, ProjectId: "proj-1", DeployKey: "SECRET"}, nil
	})

	got, err := c.Project(t.Context(), "github.com/acme/pay")
	require.NoError(t, err)
	require.Equal(t, "proj-1", got)
	require.Equal(t, "github.com/acme/pay", asked)
}

// The code is preserved rather than flattened: "this cloud does not know that
// repository" is a push to refuse, and "auth is unreachable" is a push to
// retry, and they are the same string.
func TestAnUnregisteredRepositoryStaysNotFound(t *testing.T) {
	c := newRepositories(t, func(string) (*authv1.Repository, error) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such repository"))
	})

	_, err := c.Project(t.Context(), "github.com/acme/pay")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestARepositoryWithNoProjectIsAFailedPrecondition(t *testing.T) {
	c := newRepositories(t, func(key string) (*authv1.Repository, error) {
		return &authv1.Repository{RepositoryKey: key}, nil
	})

	_, err := c.Project(t.Context(), "github.com/acme/pay")
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}

func TestProjectRefusesAnEmptyKeyWithoutDialling(t *testing.T) {
	c := newRepositories(t, func(string) (*authv1.Repository, error) {
		t.Fatal("an empty key must not reach the wire")
		return nil, nil
	})

	_, err := c.Project(t.Context(), "")
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

var _ deploy.Credentials = (*RepositoryClient)(nil)

// A DEPLOY KEY IS PER REPOSITORY: one cloud serves repositories belonging to
// people who do not know each other, and an account-scoped token leaked from
// one build would be a key to every repository in that account.
func TestDeployKey(t *testing.T) {
	c := newRepositories(t, func(key string) (*authv1.Repository, error) {
		return &authv1.Repository{
			RepositoryKey: key,
			ProjectId:     "pay",
			DeployKey:     "-----BEGIN OPENSSH PRIVATE KEY-----\nnope\n-----END OPENSSH PRIVATE KEY-----\n",
		}, nil
	})

	key, err := c.DeployKey(t.Context(), "github.com/acme/pay-config")
	require.NoError(t, err)
	require.Contains(t, key, "OPENSSH PRIVATE KEY")

	// NO KEY IS NOT AN ERROR. It is the right answer for a public repository,
	// and the clone then proceeds unauthenticated.
	public := newRepositories(t, func(key string) (*authv1.Repository, error) {
		return &authv1.Repository{RepositoryKey: key, ProjectId: "pay"}, nil
	})
	key, err = public.DeployKey(t.Context(), "github.com/acme/open")
	require.NoError(t, err)
	require.Empty(t, key)
}

func TestDeployKeyRefusesWhatItCannotAsk(t *testing.T) {
	c := newRepositories(t, func(string) (*authv1.Repository, error) {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("auth is down"))
	})

	_, err := c.DeployKey(t.Context(), "")
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// AUTH FAILING IS NOT A PUBLIC REPOSITORY. Falling back to an
	// unauthenticated clone would turn an outage into a build that cannot see
	// the code and reports the repository empty.
	_, err = c.DeployKey(t.Context(), "github.com/acme/pay-config")
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}
