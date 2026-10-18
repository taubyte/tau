package clients

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/discovery"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/services/build/artifact"
	"github.com/taubyte/tau/services/build/deploy"
	"github.com/taubyte/tau/services/build/frontend"
)

var _ frontend.Projects = (*ProjectClient)(nil)

func newProjects(t *testing.T, get func(string) (*authv2.Project, error)) *ProjectClient {
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
	c := newProjects(t, func(id string) (*authv2.Project, error) {
		return &authv2.Project{
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
	c := newProjects(t, func(id string) (*authv2.Project, error) {
		return &authv2.Project{Id: id, CodeRepositoryKey: "github.com/acme/pay"}, nil
	})

	got, err := c.Repositories(t.Context(), "proj-1")

	require.NoError(t, err)
	require.Equal(t, []string{"github.com/acme/pay"}, got)
}

// A project nobody can be shown to own is refused here rather than becoming an
// empty allow-list the caller has to remember denies.
func TestAProjectWithNoRepositoriesIsRefused(t *testing.T) {
	c := newProjects(t, func(id string) (*authv2.Project, error) {
		return &authv2.Project{Id: id}, nil
	})

	_, err := c.Repositories(t.Context(), "proj-1")
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}

func TestAnUnknownProjectStaysNotFound(t *testing.T) {
	c := newProjects(t, func(string) (*authv2.Project, error) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such project"))
	})

	_, err := c.Repositories(t.Context(), "proj-1")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestRepositoriesRefusesAnEmptyProjectIdWithoutDialling(t *testing.T) {
	c := newProjects(t, func(string) (*authv2.Project, error) {
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

	p, err := NewPool(newHost(t, mn), discovery.Static(), "/tau/build/v2", 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	c, err = Projects(p)
	require.ErrorContains(t, err, "/tau/auth/v2")
	require.Nil(t, c)
}

var _ deploy.Projects = (*ProjectClient)(nil)

// A DEPLOY ASKS A QUESTION WITH ONE RIGHT ANSWER rather than reading the pair:
// is this repository the one whose pushes are deploys.
func TestConfigures(t *testing.T) {
	c := newProjects(t, func(id string) (*authv2.Project, error) {
		return &authv2.Project{
			Id:                  id,
			ConfigRepositoryKey: "github.com/acme/pay-config",
			CodeRepositoryKey:   "github.com/acme/pay-code",
		}, nil
	})

	configures, err := c.Configures(t.Context(), "pay", "github.com/acme/pay-config")
	require.NoError(t, err)
	require.True(t, configures)

	// THE CODE REPOSITORY IS false AND NOT AN ERROR. A push to it is a build,
	// which is a different job and not a failure of this one.
	configures, err = c.Configures(t.Context(), "pay", "github.com/acme/pay-code")
	require.NoError(t, err)
	require.False(t, configures)

	// A repository the project does not name at all.
	configures, err = c.Configures(t.Context(), "pay", "github.com/acme/somebody-elses")
	require.NoError(t, err)
	require.False(t, configures)
}

func TestConfiguresRefusesWhatItCannotAsk(t *testing.T) {
	c := newProjects(t, func(string) (*authv2.Project, error) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such project"))
	})

	_, err := c.Configures(t.Context(), "", "github.com/acme/pay-config")
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	_, err = c.Configures(t.Context(), "pay", "")
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// AUTH FAILING IS NOT "THIS IS A CODE REPOSITORY", and the code survives so
	// the caller can tell a push to refuse from a push to retry.
	_, err = c.Configures(t.Context(), "pay", "github.com/acme/pay-config")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

var _ artifact.Projects = (*ProjectClient)(nil)

func TestCodeRepository(t *testing.T) {
	c := newProjects(t, func(id string) (*authv2.Project, error) {
		return &authv2.Project{
			Id:                  id,
			ConfigRepositoryKey: "github.com/acme/pay-config",
			CodeRepositoryKey:   "github.com/acme/pay-code",
		}, nil
	})

	key, err := c.CodeRepository(t.Context(), "pay")
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/pay-code", key)

	// A PROJECT WITH NONE REGISTERED IS AN EMPTY STRING AND NOT AN ERROR. It is
	// a real state — a project can be created with only a configuration
	// repository — and it means no bare-path resource is built by any push,
	// which is the right answer rather than a failure on a push that was never
	// going to build anything.
	none := newProjects(t, func(id string) (*authv2.Project, error) {
		return &authv2.Project{Id: id, ConfigRepositoryKey: "github.com/acme/pay-config"}, nil
	})
	key, err = none.CodeRepository(t.Context(), "pay")
	require.NoError(t, err)
	require.Empty(t, key)

	_, err = c.CodeRepository(t.Context(), "")
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
