package node

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

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

	l := linkSecrets{projects: p, vault: secretsMap{}}
	_, err = l.DeployKey(ctx, linkedCode)
	require.ErrorIs(t, err, ErrNoRegister)
	_, err = l.Hook(ctx, linkedID)
	require.ErrorIs(t, err, ErrNoRegister)
}

// A PUSH IS VERIFIED FROM THE LINK ALONE: the register's record and the secret
// the developer stored, before anything is published.
func TestAHookIsTheLinkAndItsSecret(t *testing.T) {
	p := linkedNode(t)
	ctx := t.Context()

	vault := secretsMap{}
	l := linkSecrets{projects: p, vault: vault}

	_, err := l.Hook(ctx, linkedID)
	require.ErrorIs(t, err, frontend.ErrNoHook, "linked with no secret set is not a hook")
	_, err = l.Hook(ctx, "QmNobody")
	require.ErrorIs(t, err, frontend.ErrNoHook)

	vault[[3]string{linkedID, linkcreds.Environment, linkcreds.HookSecret}] = "s3cret"
	hook, err := l.Hook(ctx, linkedID)
	require.NoError(t, err)
	require.Equal(t, frontend.Hook{Project: linkedID, Repositories: []string{linkedCfg, linkedCode}, Secret: []byte("s3cret")}, hook)

	_, err = linkSecrets{projects: p, vault: brokenVault{}}.Hook(ctx, linkedID)
	require.Error(t, err)
	require.NotErrorIs(t, err, frontend.ErrNoHook, "an unreachable vault is not an answer")

	_, err = linkSecrets{projects: p}.Hook(ctx, linkedID)
	require.ErrorIs(t, err, ErrNoSecrets)
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
