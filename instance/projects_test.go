package instance

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/linkcreds"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/build/frontend"
	secretsService "github.com/taubyte/tau/services/secrets"
)

// secretsMap is a vault: (project, environment, name) to value.
type secretsMap map[[3]string]string

func (m secretsMap) Secret(_ context.Context, project, environment, name string) (string, error) {
	v, ok := m[[3]string{project, environment, name}]
	if !ok {
		return "", secretsService.ErrNotSet
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

func linkedNode(t *testing.T) projectTable {
	t.Helper()
	n, _, _ := makeNode(t, Config{Services: []string{Register, Discovery}})
	require.NoError(t, n.Start(t.Context()))
	p := projectTable{n}
	_, err := p.LinkProject(t.Context(), pkgregister.Link{
		Project: linkedID, Config: linkedCfg, Code: []string{linkedCode}, Claimant: "github.com/1",
	})
	require.NoError(t, err)
	return p
}

func TestTheProjectTableAnswersBothBuildRoles(t *testing.T) {
	p := linkedNode(t)
	ctx := t.Context()

	id, err := p.Project(ctx, linkedCode)
	require.NoError(t, err)
	require.Equal(t, linkedID, id)

	_, err = p.Project(ctx, "github.com/nobody/here")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "the backend reads an unlinked repository as NotFound")

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
	require.ErrorIs(t, err, pkgregister.ErrNotLinked)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	mine, err := p.ProjectsOf(ctx, "github.com/1")
	require.NoError(t, err)
	require.Len(t, mine, 1)

	_, err = p.LinkProject(ctx, pkgregister.Link{Project: "QmConfigOnly", Config: "github.com/acme/other", Claimant: "github.com/1"})
	require.NoError(t, err)
	// SEVERAL CODE REPOSITORIES, in an order that is not sorted: the bare
	// path is still the configuration repository, never the first of them.
	_, err = p.LinkProject(ctx, pkgregister.Link{Project: "QmManyCode", Config: "github.com/acme/many-config",
		Code: []string{"github.com/acme/zeta", "github.com/acme/alpha", "github.com/acme/mid"}, Claimant: "github.com/1"})
	require.NoError(t, err)
	main, err = p.MainRepository(ctx, "QmManyCode")
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/many-config", main)
	_, err = p.MainRepository(ctx, "QmNobody")
	require.Error(t, err)
}

func TestTheProjectTableHoldsRoles(t *testing.T) {
	p := linkedNode(t)
	ctx := t.Context()

	role, err := p.Role(ctx, linkedID, "github.com/1")
	require.NoError(t, err)
	require.Equal(t, pkgregister.RoleOwner, role)
	_, err = p.Role(ctx, "QmNobody", "github.com/1")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	g, err := p.GrantRole(ctx, linkedID, "bob", pkgregister.RoleBuilder, "github.com/1")
	require.NoError(t, err)
	role, err = p.Role(ctx, linkedID, "bob")
	require.NoError(t, err)
	require.Equal(t, pkgregister.RoleBuilder, role)
	grants, err := p.Grants(ctx, linkedID)
	require.NoError(t, err)
	require.Len(t, grants, 1)

	require.NoError(t, p.Revoke(ctx, linkedID, g.ID, "github.com/1"))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(p.Revoke(ctx, linkedID, "nope", "github.com/1")))
	role, err = p.Role(ctx, linkedID, "bob")
	require.NoError(t, err)
	require.Equal(t, pkgregister.RoleNone, role)
}

func TestWithNoRegisterAnywhereTheTableSaysSo(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Discovery}})
	require.NoError(t, n.Start(t.Context()))
	p := projectTable{n}
	ctx := t.Context()

	_, err := p.Project(ctx, linkedCode)
	require.ErrorIs(t, err, ErrNoRegister)
	_, err = p.Configures(ctx, linkedID, linkedCfg)
	require.ErrorIs(t, err, ErrNoRegister)
	_, err = p.Repositories(ctx, linkedID)
	require.ErrorIs(t, err, ErrNoRegister)
	_, err = p.LinkProject(ctx, pkgregister.Link{Project: linkedID, Config: linkedCfg, Claimant: "x"})
	require.ErrorIs(t, err, ErrNoRegister)
	_, err = p.ProjectsOf(ctx, "x")
	require.ErrorIs(t, err, ErrNoRegister)
	_, err = p.LinkedRepositories(ctx)
	require.ErrorIs(t, err, ErrNoRegister)
	_, err = p.Role(ctx, linkedID, "x")
	require.ErrorIs(t, err, ErrNoRegister)
	_, err = p.GrantRole(ctx, linkedID, "x", pkgregister.RoleViewer, "y")
	require.ErrorIs(t, err, ErrNoRegister)
	require.ErrorIs(t, p.Revoke(ctx, linkedID, "id", "y"), ErrNoRegister)
	_, err = p.Grants(ctx, linkedID)
	require.ErrorIs(t, err, ErrNoRegister)

	l := linkSecrets{projects: p, vault: secretsMap{}}
	_, err = l.DeployKey(ctx, linkedCode)
	require.ErrorIs(t, err, ErrNoRegister)
	_, err = l.Hook(ctx, linkedID)
	require.ErrorIs(t, err, ErrNoRegister)
}

func TestAHookIsTheLinkAndItsSecret(t *testing.T) {
	p := linkedNode(t)
	ctx := t.Context()

	vault := secretsMap{}
	l := linkSecrets{projects: p, vault: vault}

	_, err := l.Hook(ctx, linkedID)
	require.ErrorIs(t, err, frontend.ErrNoHook, "linked with no secret set is not a hook")
	_, err = l.Hook(ctx, "QmNobody")
	require.ErrorIs(t, err, frontend.ErrNoHook)

	// EACH REPOSITORY HAS ITS OWN SECRET, and one with none is left out.
	vault[[3]string{linkedID, linkcreds.Environment, linkcreds.HookSecret(linkedCode)}] = "code-secret"
	hook, err := l.Hook(ctx, linkedID)
	require.NoError(t, err)
	require.Equal(t, frontend.Hook{Project: linkedID, Repositories: []frontend.HookRepository{{Key: linkedCode, Secret: []byte("code-secret")}}}, hook)

	vault[[3]string{linkedID, linkcreds.Environment, linkcreds.HookSecret(linkedCfg)}] = "cfg-secret"
	hook, err = l.Hook(ctx, linkedID)
	require.NoError(t, err)
	require.Equal(t, frontend.Hook{Project: linkedID, Repositories: []frontend.HookRepository{
		{Key: linkedCfg, Secret: []byte("cfg-secret")}, {Key: linkedCode, Secret: []byte("code-secret")},
	}}, hook)

	_, err = linkSecrets{projects: p, vault: brokenVault{}}.Hook(ctx, linkedID)
	require.Error(t, err)
	require.NotErrorIs(t, err, frontend.ErrNoHook, "an unreachable vault is not an answer")

	_, err = linkSecrets{projects: p}.Hook(ctx, linkedID)
	require.ErrorIs(t, err, secretsService.ErrNoSecrets)
}

func TestADeployKeyIsReadFromTheVaultByRepository(t *testing.T) {
	p := linkedNode(t)
	ctx := t.Context()

	vault := secretsMap{}
	l := linkSecrets{projects: p, vault: vault}

	key, err := l.DeployKey(ctx, linkedCode)
	require.NoError(t, err)
	require.Empty(t, key, "none set clones with none — a public repository")

	key, err = l.DeployKey(ctx, "github.com/nobody/here")
	require.NoError(t, err)
	require.Empty(t, key)

	vault[[3]string{linkedID, linkcreds.Environment, linkcreds.DeployKey(linkedCode)}] = "-----BEGIN OPENSSH PRIVATE KEY-----"
	key, err = l.DeployKey(ctx, "github.com/ACME/code")
	require.NoError(t, err)
	require.Equal(t, "-----BEGIN OPENSSH PRIVATE KEY-----", key)

	_, err = linkSecrets{projects: p, vault: brokenVault{}}.DeployKey(ctx, linkedCode)
	require.Error(t, err)
}

func TestLinkedRepositoriesIsEveryRepositoryOfEveryProject(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Discovery}})
	require.NoError(t, n.Start(t.Context()))
	p := projectTable{n}
	ctx := t.Context()

	repos, err := p.LinkedRepositories(ctx)
	require.NoError(t, err)
	require.Empty(t, repos, "an empty table links nothing")

	_, err = p.LinkProject(ctx, pkgregister.Link{Project: linkedID, Config: linkedCfg, Code: []string{linkedCode}, Claimant: "github.com/1"})
	require.NoError(t, err)
	_, err = p.LinkProject(ctx, pkgregister.Link{Project: "QmConfigOnly", Config: "github.com/acme/other", Claimant: "github.com/2"})
	require.NoError(t, err)

	repos, err = p.LinkedRepositories(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{linkedCfg, linkedCode, "github.com/acme/other"}, repos)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = p.LinkedRepositories(cancelled)
	require.ErrorIs(t, err, context.Canceled, "a listing that cannot finish is not an empty one")

	// A table the register cannot scan is an error, not an empty listing a
	// resync would read as "nothing to compare".
	broken := errors.New("store unreachable")
	local := n.register.Local()
	n.registers = rpc.Reach(n.Host(), Register, func() (libp2ppeer.ID, error) { return "", ErrNoRegister },
		func() (registerSeam, bool) { return unlisting{registerSeam: local, err: broken}, true },
		func(libp2ppeer.ID) (registerSeam, error) { return nil, ErrNoRegister })
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
		return "", time.Time{}, secretsService.ErrNotSet
	}
	return v, h.replaced, nil
}

func TestAHookCarriesTheSecretItsSecretReplaced(t *testing.T) {
	p := linkedNode(t)
	ctx := t.Context()
	code := [3]string{linkedID, linkcreds.Environment, linkcreds.HookSecret(linkedCode)}
	cfg := [3]string{linkedID, linkcreds.Environment, linkcreds.HookSecret(linkedCfg)}
	at := time.Unix(1700000000, 0)

	vault := historyVault{
		secretsMap: secretsMap{code: "new", cfg: "cfg-secret"},
		previous:   map[[3]string]string{code: "old"},
		replaced:   at,
	}
	hook, err := linkSecrets{projects: p, vault: vault}.Hook(ctx, linkedID)
	require.NoError(t, err)
	require.Equal(t, []frontend.HookRepository{
		{Key: linkedCfg, Secret: []byte("cfg-secret")},
		{Key: linkedCode, Secret: []byte("new"), Previous: []byte("old"), Replaced: at},
	}, hook.Repositories)

	vault.err = errors.New("vault unreachable")
	_, err = linkSecrets{projects: p, vault: vault}.Hook(ctx, linkedID)
	require.ErrorContains(t, err, "vault unreachable")
}

// unlisting is a register whose project table cannot be scanned.
type unlisting struct {
	registerSeam
	err error
}

func (u unlisting) ListProjects(context.Context) ([]pkgregister.ProjectRecord, error) {
	return nil, u.err
}
