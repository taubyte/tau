package linked

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/linkcreds"
	authmock "github.com/taubyte/tau/services/auth/mock"
	"github.com/taubyte/tau/services/auth/projects"
	"github.com/taubyte/tau/services/secrets"
)

// secretsMap is a vault: (project, environment, name) to value.
type secretsMap map[[3]string]string

func (m secretsMap) Secret(_ context.Context, project, environment, name string) (string, error) {
	v, ok := m[[3]string{project, environment, name}]
	if !ok {
		return "", secrets.ErrNotSet
	}
	return v, nil
}

type brokenVault struct{}

func (brokenVault) Secret(context.Context, string, string, string) (string, error) {
	return "", errors.New("vault unreachable")
}

const (
	linkedID   = "QmLinkedProject"
	linkedCfg  = "github.com/acme/config"
	linkedCode = "github.com/acme/code"
)

func table(t *testing.T) (Projects, projects.Projects) {
	t.Helper()
	r := authmock.New().Projects()
	return Projects{Table: r}, r
}

func linkedTable(t *testing.T) Projects {
	t.Helper()
	p, _ := table(t)
	_, err := p.LinkProject(t.Context(), projects.Link{
		Project: linkedID, Config: linkedCfg, Code: []string{linkedCode}, Claimant: "github.com/1",
	})
	require.NoError(t, err)
	return p
}

func TestTheProjectTableAnswersBothBuildRoles(t *testing.T) {
	p := linkedTable(t)
	ctx := t.Context()

	id, err := p.Project(ctx, linkedCode)
	require.NoError(t, err)
	require.Equal(t, linkedID, id)

	_, err = p.Project(ctx, "github.com/nobody/here")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "plm reads an unlinked repository as NotFound")

	for repo, want := range map[string]bool{linkedCfg: true, linkedCode: false, "github.com/nobody/here": false} {
		got, err := p.Configures(ctx, linkedID, repo)
		require.NoError(t, err)
		require.Equal(t, want, got, repo)
	}
	got, err := p.Configures(ctx, "QmAnotherProject", linkedCfg)
	require.NoError(t, err)
	require.False(t, got, "configuring one project is not configuring another")

	main, err := p.MainRepository(ctx, linkedID)
	require.NoError(t, err)
	require.Equal(t, linkedCfg, main, "a bare path is the configuration repository, not a code one")

	repos, err := p.Repositories(ctx, linkedID)
	require.NoError(t, err)
	require.Equal(t, []string{linkedCfg, linkedCode}, repos)

	_, err = p.Repositories(ctx, "QmNobody")
	require.ErrorIs(t, err, projects.ErrNotLinked)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	mine, err := p.ProjectsOf(ctx, "github.com/1")
	require.NoError(t, err)
	require.Len(t, mine, 1)

	// SEVERAL CODE REPOSITORIES, in an order that is not sorted: the bare path is
	// still the configuration repository, never the first of them.
	_, err = p.LinkProject(ctx, projects.Link{Project: "QmManyCode", Config: "github.com/acme/many-config",
		Code: []string{"github.com/acme/zeta", "github.com/acme/alpha", "github.com/acme/mid"}, Claimant: "github.com/1"})
	require.NoError(t, err)
	main, err = p.MainRepository(ctx, "QmManyCode")
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/many-config", main)
	_, err = p.MainRepository(ctx, "QmNobody")
	require.Error(t, err)
}

func TestTheProjectTableHoldsRoles(t *testing.T) {
	p := linkedTable(t)
	ctx := t.Context()

	role, err := p.Role(ctx, linkedID, "github.com/1")
	require.NoError(t, err)
	require.Equal(t, projects.RoleOwner, role)
	_, err = p.Role(ctx, "QmNobody", "github.com/1")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	g, err := p.GrantRole(ctx, linkedID, "bob", projects.RoleBuilder, "github.com/1")
	require.NoError(t, err)
	role, err = p.Role(ctx, linkedID, "bob")
	require.NoError(t, err)
	require.Equal(t, projects.RoleBuilder, role)
	grants, err := p.Grants(ctx, linkedID)
	require.NoError(t, err)
	require.Len(t, grants, 1)

	require.NoError(t, p.Revoke(ctx, linkedID, g.ID, "github.com/1"))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(p.Revoke(ctx, linkedID, "nope", "github.com/1")))
	role, err = p.Role(ctx, linkedID, "bob")
	require.NoError(t, err)
	require.Equal(t, projects.RoleNone, role)
}

// unreached is a table no node answers for.
type unreached struct{ err error }

func (u unreached) LinkProject(context.Context, projects.Link) (projects.ProjectRecord, error) {
	return projects.ProjectRecord{}, u.err
}

func (u unreached) Project(context.Context, string) (projects.ProjectRecord, error) {
	return projects.ProjectRecord{}, u.err
}

func (u unreached) Repository(context.Context, string) (projects.RepositoryRecord, error) {
	return projects.RepositoryRecord{}, u.err
}

func (u unreached) ProjectsOf(context.Context, string) ([]projects.ProjectRecord, error) {
	return nil, u.err
}

func (u unreached) ListProjects(context.Context) ([]projects.ProjectRecord, error) {
	return nil, u.err
}

func (u unreached) GrantRole(context.Context, string, string, projects.Role, string) (projects.Grant, error) {
	return projects.Grant{}, u.err
}

func (u unreached) Revoke(context.Context, string, string, string) error { return u.err }

func (u unreached) Grants(context.Context, string) ([]projects.Grant, error) { return nil, u.err }

func (u unreached) RoleOf(context.Context, string, string) (projects.Role, error) {
	return projects.RoleNone, u.err
}

func TestWithNoAuthTheTableSaysSo(t *testing.T) {
	gone := errors.New("auth is not reachable")
	p := Projects{Table: unreached{gone}}
	ctx := t.Context()

	_, err := p.Project(ctx, linkedCode)
	require.ErrorIs(t, err, gone)
	_, err = p.Configures(ctx, linkedID, linkedCfg)
	require.ErrorIs(t, err, gone)
	_, err = p.Repositories(ctx, linkedID)
	require.ErrorIs(t, err, gone)
	_, err = p.LinkProject(ctx, projects.Link{Project: linkedID, Config: linkedCfg, Claimant: "x"})
	require.ErrorIs(t, err, gone)
	_, err = p.ProjectsOf(ctx, "x")
	require.ErrorIs(t, err, gone)
	_, err = p.LinkedRepositories(ctx)
	require.ErrorIs(t, err, gone)
	_, err = p.Role(ctx, linkedID, "x")
	require.ErrorIs(t, err, gone)
	_, err = p.GrantRole(ctx, linkedID, "x", projects.RoleViewer, "y")
	require.ErrorIs(t, err, gone)
	require.ErrorIs(t, p.Revoke(ctx, linkedID, "id", "y"), gone)
	_, err = p.Grants(ctx, linkedID)
	require.ErrorIs(t, err, gone)

	_, err = Credentials{Projects: p, Vault: secretsMap{}}.DeployKey(ctx, linkedCode)
	require.ErrorIs(t, err, gone)
}

func TestADeployKeyIsReadFromTheVaultByRepository(t *testing.T) {
	p := linkedTable(t)
	ctx := t.Context()

	vault := secretsMap{}
	c := Credentials{Projects: p, Vault: vault}

	key, err := c.DeployKey(ctx, linkedCode)
	require.NoError(t, err)
	require.Empty(t, key, "none set clones with none — a public repository")

	key, err = c.DeployKey(ctx, "github.com/nobody/here")
	require.NoError(t, err)
	require.Empty(t, key)

	vault[[3]string{linkedID, linkcreds.Environment, linkcreds.DeployKey(linkedCode)}] = "-----BEGIN OPENSSH PRIVATE KEY-----"
	key, err = c.DeployKey(ctx, "github.com/ACME/code")
	require.NoError(t, err)
	require.Equal(t, "-----BEGIN OPENSSH PRIVATE KEY-----", key)

	_, err = Credentials{Projects: p, Vault: brokenVault{}}.DeployKey(ctx, linkedCode)
	require.Error(t, err)

	_, _, err = Credentials{Projects: p}.Value(ctx, linkedID, "anything")
	require.ErrorIs(t, err, secrets.ErrNoSecrets, "no vault is not an unset secret")
}

func TestLinkedRepositoriesIsEveryRepositoryOfEveryProject(t *testing.T) {
	p, r := table(t)
	ctx := t.Context()

	repos, err := p.LinkedRepositories(ctx)
	require.NoError(t, err)
	require.Empty(t, repos, "an empty table links nothing")

	_, err = p.LinkProject(ctx, projects.Link{Project: linkedID, Config: linkedCfg, Code: []string{linkedCode}, Claimant: "github.com/1"})
	require.NoError(t, err)
	_, err = p.LinkProject(ctx, projects.Link{Project: "QmConfigOnly", Config: "github.com/acme/other", Claimant: "github.com/2"})
	require.NoError(t, err)

	repos, err = p.LinkedRepositories(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{linkedCfg, linkedCode, "github.com/acme/other"}, repos)

	// A table the register cannot scan is an error, not an empty listing a resync
	// would read as "nothing to compare".
	broken := errors.New("store unreachable")
	p.Table = unlisting{Projects: r, err: broken}
	repos, err = p.LinkedRepositories(ctx)
	require.ErrorIs(t, err, broken)
	require.Nil(t, repos)
}

// historyVault also serves what each secret replaced.
type historyVault struct {
	secretsMap
	previous map[[3]string]string
	replaced time.Time
	err      error
}

func (h historyVault) Previous(_ context.Context, project, environment, name string) (string, time.Time, error) {
	if h.err != nil {
		return "", time.Time{}, h.err
	}
	v, ok := h.previous[[3]string{project, environment, name}]
	if !ok {
		return "", time.Time{}, secrets.ErrNotSet
	}
	return v, h.replaced, nil
}

func TestACredentialCarriesWhatItReplaced(t *testing.T) {
	p := linkedTable(t)
	ctx := t.Context()
	name := linkcreds.HookSecret(linkedCode)
	at := time.Unix(1700000000, 0)

	vault := historyVault{
		secretsMap: secretsMap{{linkedID, linkcreds.Environment, name}: "new"},
		previous:   map[[3]string]string{{linkedID, linkcreds.Environment, name}: "old"},
		replaced:   at,
	}
	c := Credentials{Projects: p, Vault: vault}
	v, found, err := c.Value(ctx, linkedID, name)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("new"), v)
	old, when, err := c.Previous(ctx, linkedID, name)
	require.NoError(t, err)
	require.Equal(t, []byte("old"), old)
	require.Equal(t, at, when)

	old, _, err = c.Previous(ctx, linkedID, "unset")
	require.NoError(t, err)
	require.Nil(t, old, "nothing replaced is nothing")

	old, _, err = Credentials{Projects: p, Vault: secretsMap{}}.Previous(ctx, linkedID, name)
	require.NoError(t, err)
	require.Nil(t, old, "a vault that keeps no history replaced nothing")

	vault.err = errors.New("vault unreachable")
	_, _, err = Credentials{Projects: p, Vault: vault}.Previous(ctx, linkedID, name)
	require.ErrorContains(t, err, "vault unreachable")
}

// unlisting is a project table that cannot be scanned.
type unlisting struct {
	projects.Projects
	err error
}

func (u unlisting) ListProjects(context.Context) ([]projects.ProjectRecord, error) {
	return nil, u.err
}
