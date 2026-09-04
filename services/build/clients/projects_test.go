package clients

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/discovery"
	authv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/auth/v1"
	"github.com/taubyte/tau/services/build/frontend"
)

var _ frontend.Projects = (*ProjectClient)(nil)

func newProjects(t *testing.T, get func(string) (*authv1.Project, error)) *ProjectClient {
	t.Helper()
	mn := newMocknet(t)
	caller := newHost(t, mn)
	server := serveProjects(t, mn, get)

	p, err := NewPool(caller, discovery.Static(server.ID()), AuthProtocol, 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	c, err := Projects(p)
	require.NoError(t, err)
	return c
}

// Which repositories gate a project is auth's schema knowledge; that it is
// currently exactly two of them is not something the frontend should observe.
func TestRepositoriesReturnsBothKeysAsOneList(t *testing.T) {
	c := newProjects(t, func(id string) (*authv1.Project, error) {
		return &authv1.Project{
			Id:                  id,
			ConfigRepositoryKey: "github.com/acme/pay-config",
			CodeRepositoryKey:   "github.com/acme/pay-code",
		}, nil
	})

	got, err := c.Repositories(t.Context(), "proj-1")

	require.NoError(t, err)
	require.Equal(t, []string{"github.com/acme/pay-config", "github.com/acme/pay-code"}, got)
}

// An empty key matches no repository, and a caller that saw one would have to
// know that.
func TestAnEmptyKeyIsDroppedRatherThanReturned(t *testing.T) {
	c := newProjects(t, func(id string) (*authv1.Project, error) {
		return &authv1.Project{Id: id, CodeRepositoryKey: "github.com/acme/pay"}, nil
	})

	got, err := c.Repositories(t.Context(), "proj-1")

	require.NoError(t, err)
	require.Equal(t, []string{"github.com/acme/pay"}, got)
}

// A project nobody can be shown to own is refused here rather than becoming an
// empty allow-list the caller has to remember denies.
func TestAProjectWithNoRepositoriesIsRefused(t *testing.T) {
	c := newProjects(t, func(id string) (*authv1.Project, error) {
		return &authv1.Project{Id: id}, nil
	})

	_, err := c.Repositories(t.Context(), "proj-1")
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}

func TestAnUnknownProjectStaysNotFound(t *testing.T) {
	c := newProjects(t, func(string) (*authv1.Project, error) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such project"))
	})

	_, err := c.Repositories(t.Context(), "proj-1")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestRepositoriesRefusesAnEmptyProjectIdWithoutDialling(t *testing.T) {
	c := newProjects(t, func(string) (*authv1.Project, error) {
		t.Fatal("an empty project id must not reach the wire")
		return nil, nil
	})

	_, err := c.Repositories(t.Context(), "")
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestProjectsRefusesAPoolForTheWrongProtocol(t *testing.T) {
	mn := newMocknet(t)

	c, err := Projects(nil)
	require.Error(t, err)
	require.Nil(t, c)

	p, err := NewPool(newHost(t, mn), discovery.Static(), "/tau/build/v1", 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	c, err = Projects(p)
	require.ErrorContains(t, err, "/tau/auth/v1")
	require.Nil(t, c)
}
