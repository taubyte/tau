package hosted

import (
	"context"
	"errors"
	"testing"
	"time"

	authmock "github.com/taubyte/tau/services/auth/mock"
	"github.com/taubyte/tau/services/auth/projects"
	"github.com/taubyte/tau/services/plm/linked"
	"github.com/taubyte/tau/services/register"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/linkcreds"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/plm/ingress"
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

func linkedTable(t *testing.T) linked.Projects {
	t.Helper()
	p := linked.Projects{Table: authmock.New().Projects()}
	_, err := p.LinkProject(t.Context(), projects.Link{
		Project: linkedID, Config: linkedCfg, Code: []string{linkedCode}, Claimant: "github.com/1",
	})
	require.NoError(t, err)
	return p
}

func TestAHookIsTheLinkAndItsSecret(t *testing.T) {
	p := linkedTable(t)
	ctx := t.Context()

	vault := secretsMap{}
	l := hooks{linked.Credentials{Projects: p, Vault: vault}}

	_, err := l.Hook(ctx, linkedID)
	require.ErrorIs(t, err, ingress.ErrNoHook, "linked with no secret set is not a hook")
	_, err = l.Hook(ctx, "QmNobody")
	require.ErrorIs(t, err, ingress.ErrNoHook)

	// EACH REPOSITORY HAS ITS OWN SECRET, and one with none is left out.
	vault[[3]string{linkedID, linkcreds.Environment, linkcreds.HookSecret(linkedCode)}] = "code-secret"
	hook, err := l.Hook(ctx, linkedID)
	require.NoError(t, err)
	require.Equal(t, ingress.Hook{Project: linkedID, Repositories: []ingress.HookRepository{{Key: linkedCode, Secret: []byte("code-secret")}}}, hook)

	vault[[3]string{linkedID, linkcreds.Environment, linkcreds.HookSecret(linkedCfg)}] = "cfg-secret"
	hook, err = l.Hook(ctx, linkedID)
	require.NoError(t, err)
	require.Equal(t, ingress.Hook{Project: linkedID, Repositories: []ingress.HookRepository{
		{Key: linkedCfg, Secret: []byte("cfg-secret")}, {Key: linkedCode, Secret: []byte("code-secret")},
	}}, hook)

	_, err = hooks{linked.Credentials{Projects: p, Vault: brokenVault{}}}.Hook(ctx, linkedID)
	require.Error(t, err)
	require.NotErrorIs(t, err, ingress.ErrNoHook, "an unreachable vault is not an answer")

	_, err = hooks{linked.Credentials{Projects: p}}.Hook(ctx, linkedID)
	require.ErrorIs(t, err, secretsService.ErrNoSecrets)
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
	p := linkedTable(t)
	ctx := t.Context()
	code := [3]string{linkedID, linkcreds.Environment, linkcreds.HookSecret(linkedCode)}
	cfg := [3]string{linkedID, linkcreds.Environment, linkcreds.HookSecret(linkedCfg)}
	at := time.Unix(1700000000, 0)

	vault := historyVault{
		secretsMap: secretsMap{code: "new", cfg: "cfg-secret"},
		previous:   map[[3]string]string{code: "old"},
		replaced:   at,
	}
	hook, err := hooks{linked.Credentials{Projects: p, Vault: vault}}.Hook(ctx, linkedID)
	require.NoError(t, err)
	require.Equal(t, []ingress.HookRepository{
		{Key: linkedCfg, Secret: []byte("cfg-secret")},
		{Key: linkedCode, Secret: []byte("new"), Previous: []byte("old"), Replaced: at},
	}, hook.Repositories)

	vault.err = errors.New("vault unreachable")
	_, err = hooks{linked.Credentials{Projects: p, Vault: vault}}.Hook(ctx, linkedID)
	require.ErrorContains(t, err, "vault unreachable")
}

func TestWhatIsPublishedIsReadBackFromTheRegister(t *testing.T) {
	r, err := pkgregister.New(pkgregister.WithStore(pkgregister.NewMemStore()))
	require.NoError(t, err)
	p := published{table: func(context.Context) (register.Table, error) { return r, nil }}

	// A project nobody published has no deployments, and asking is not an error.
	got, err := p.Environments(t.Context(), "shop")
	require.NoError(t, err)
	require.Empty(t, got)

	compiled := []byte(`{"object":{"id":"QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zd","name":"shop"}}`)
	_, err = r.Publish(t.Context(), pkgregister.Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled})
	require.NoError(t, err)

	got, err = p.Environments(t.Context(), "shop")
	require.NoError(t, err)
	require.Equal(t, []string{"prod"}, got)
	read, err := p.Compiled(t.Context(), "shop", "prod")
	require.NoError(t, err)
	require.JSONEq(t, string(compiled), string(read))
	_, err = p.Compiled(t.Context(), "shop", "preview")
	require.Error(t, err)

	// With no register to ask, it fails rather than answering nothing.
	gone := errors.New("no register is reachable")
	none := published{table: func(context.Context) (register.Table, error) { return nil, gone }}
	_, err = none.Environments(t.Context(), "shop")
	require.ErrorIs(t, err, gone)
	_, err = none.Compiled(t.Context(), "shop", "prod")
	require.ErrorIs(t, err, gone)
}
