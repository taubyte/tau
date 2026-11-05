package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/pkg/forge/drivers"
	"github.com/taubyte/tau/pkg/git/gittest"
	"github.com/taubyte/tau/pkg/linkcreds"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/build/linked"
	"github.com/taubyte/tau/services/build/run"
	"github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/secrets"
)

// node is an instance as a runner sees it: a peer, what runs beside it, and the
// section an operator wrote.
type node struct {
	peer.Node
	running []service.Running
	section map[string]any
}

func (n *node) Peer() peer.Node            { return n.Node }
func (n *node) Running() []service.Running { return n.running }
func (*node) Root() string                 { return "" }
func (*node) Cloud() string                { return "test" }
func (*node) Region() string               { return "" }
func (*node) Now() time.Time               { return time.Now() }

func (n *node) Config(into any) error {
	raw, err := yaml.Marshal(n.section)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(raw, into)
}

func lone(t *testing.T, section map[string]any) *node {
	t.Helper()
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	return &node{Node: p, section: section}
}

func TestARunnerRefusesWhatItCannotBuildWith(t *testing.T) {
	for name, section := range map[string]map[string]any{
		"a daemon nothing opens":       {"container": map[string]any{"backend": "podman"}},
		"a forge with no driver":       {"forges": []any{map[string]any{"host": "code.example.com", "driver": "fossil"}}},
		"a host key that is junk":      {"forges": []any{map[string]any{"host": "git.example.com", "driver": "gitea", "hostkeys": []any{"junk"}}}},
		"a section of the wrong shape": {"local": "many"},
		"uploads it cannot keep":       {"uploads": "/dev/null/uploads"},
	} {
		t.Run(name, func(t *testing.T) {
			r := New()
			require.Error(t, r.Start(t.Context(), lone(t, section)))
			require.NoError(t, r.Close())
		})
	}
	_, err := containers.Open(containers.Declared{Backend: "podman"})
	require.ErrorIs(t, err, containers.ErrUnknownBackend)
}

func TestAnUntrustedCloudProvesZonesThroughCM(t *testing.T) {
	h := lone(t, nil)
	d, err := provers(h, Config{})
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	require.Error(t, d.Prove(t.Context(), "QmProject", "example.com"), "no cm anywhere proves nothing")

	trust, err := provers(h, Config{Domains: Domains{Trusted: true}})
	require.NoError(t, err)
	require.NoError(t, trust.Prove(t.Context(), "anything", "example.com"))
	require.NoError(t, trust.Close())
}

// secretsMap is a vault: (project, environment, name) to value.
type secretsMap map[[3]string]string

func (m secretsMap) Secret(_ context.Context, project, environment, name string) (string, error) {
	v, ok := m[[3]string{project, environment, name}]
	if !ok {
		return "", secrets.ErrNotSet
	}
	return v, nil
}

func TestARunnerConfirmsAPrivateRepositoryWithTheDeployKeyALinkStored(t *testing.T) {
	// No agent: a clone must authenticate with the vault's key or not at all.
	t.Setenv("SSH_AUTH_SOCK", "")

	deployKey, public := gittest.KeyPair(t)
	host := gittest.NewServer(t, func(k gossh.PublicKey) bool { return string(k.Marshal()) == string(public.Marshal()) })
	st, commit := gittest.Repository(t, map[string]string{"project.yaml": "id: QmPrivate\n"})
	host.Serve("/acme/private.git", st)
	repository := host.Addr + "/acme/private"

	reg, err := pkgregister.New(pkgregister.WithStore(pkgregister.NewMemStore()))
	require.NoError(t, err)
	table := func(context.Context) (register.Table, error) { return reg, nil }
	projects := linked.Projects{Table: table}
	_, err = projects.LinkProject(t.Context(), pkgregister.Link{Project: "QmPrivate", Config: repository, Claimant: "github.com/1"})
	require.NoError(t, err)

	vault := secretsMap{}
	cfg := Config{Forges: []drivers.Forge{{
		Host: host.Addr, Driver: drivers.GitHub,
		HostKeys: []string{strings.TrimSpace(string(gossh.MarshalAuthorizedKey(host.HostKey)))},
	}}}
	_, confirm, err := compose("", cfg, projects, table, vault, trusted{})
	require.NoError(t, err)

	// Nothing stored: read with no key, and refused.
	require.Error(t, confirm.Confirm(t.Context(), repository, "refs/heads/main", commit),
		"a private repository with no deploy key stored is not readable")

	// Another key stored under the name: refused by the host.
	stranger, _ := gittest.KeyPair(t)
	vault[[3]string{"QmPrivate", linkcreds.Environment, linkcreds.DeployKey(repository)}] = stranger
	require.Error(t, confirm.Confirm(t.Context(), repository, "refs/heads/main", commit), "a key the host does not admit reads nothing")

	// The link's key: the ref is read at the forge, at the pushed commit.
	vault[[3]string{"QmPrivate", linkcreds.Environment, linkcreds.DeployKey(repository)}] = deployKey
	require.NoError(t, confirm.Confirm(t.Context(), repository, "refs/heads/main", commit))
	require.ErrorIs(t, confirm.Confirm(t.Context(), repository, "refs/heads/main", strings.Repeat("0", 40)), run.ErrNotAtCommit)
}

func TestADevelopmentCloudsRunnerReadsItsMirrorAndUploads(t *testing.T) {
	uploads := t.TempDir() + "/uploads"
	h := lone(t, map[string]any{
		"uploads": uploads,
		"mirror":  "file://" + t.TempDir(),
		"domains": map[string]any{"trusted": true},
	})
	r := New()
	require.NoError(t, r.Start(t.Context(), h))
	t.Cleanup(func() { r.Close() })
	require.DirExists(t, uploads, "the uploads directory is the build's to make")
}

func TestACodeBuildReadsTheRegistersTable(t *testing.T) {
	reg, err := pkgregister.New(pkgregister.WithStore(pkgregister.NewMemStore()))
	require.NoError(t, err)
	r := tableReader{table: func(context.Context) (register.Table, error) { return reg, nil }}

	envs, err := r.Environments(t.Context(), "shop")
	require.NoError(t, err)
	require.Empty(t, envs)
	_, err = reg.Publish(t.Context(), pkgregister.Publication{Project: "shop", Environment: "prod", Revision: 1,
		Compiled: []byte(`{"object":{"id":"QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zd","name":"shop"}}`)})
	require.NoError(t, err)
	pointer, err := r.Resolve(t.Context(), "shop", "prod")
	require.NoError(t, err)
	compiled, err := r.Object(t.Context(), pointer.Object)
	require.NoError(t, err)
	require.Contains(t, string(compiled), "shop")

	gone := errors.New("no register is reachable")
	none := tableReader{table: func(context.Context) (register.Table, error) { return nil, gone }}
	_, err = none.Environments(t.Context(), "shop")
	require.ErrorIs(t, err, gone)
	_, err = none.Resolve(t.Context(), "shop", "prod")
	require.ErrorIs(t, err, gone)
	_, err = none.Object(t.Context(), "id")
	require.ErrorIs(t, err, gone)
}
