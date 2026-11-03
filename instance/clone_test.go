package instance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/git/gittest"
	"github.com/taubyte/tau/pkg/linkcreds"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

func TestTheBackendClonesAPrivateRepositoryWithTheDeployKeyALinkStored(t *testing.T) {
	// No agent: a clone must authenticate with the vault's key or not at all.
	t.Setenv("SSH_AUTH_SOCK", "")

	deployKey, public := gittest.KeyPair(t)
	host := gittest.NewServer(t, func(k gossh.PublicKey) bool { return string(k.Marshal()) == string(public.Marshal()) })
	st, commit := gittest.Repository(t, map[string]string{"project.yaml": "id: QmPrivate\n"})
	host.Serve("/acme/private.git", st)
	repository := host.Addr + "/acme/private"

	n, _, _ := makeNode(t, Config{
		Services: []string{Register, Secrets, Discovery},
		Forges: []Forge{{
			Host: host.Addr, Driver: ForgeGitHub,
			HostKeys: []string{strings.TrimSpace(string(gossh.MarshalAuthorizedKey(host.HostKey)))},
		}},
	})
	require.NoError(t, n.Start(t.Context()))
	ctx := t.Context()

	projects := projectTable{n}
	_, err := projects.LinkProject(ctx, pkgregister.Link{Project: "QmPrivate", Config: repository, Claimant: "github.com/1"})
	require.NoError(t, err)

	vault := newVault(n)
	t.Cleanup(func() { vault.Close() })
	clone, err := n.buildClone(linkSecrets{projects: projects, vault: vault})
	require.NoError(t, err)

	// stored sets the deploy key and waits until the vault reads it back.
	stored := func(key string) {
		t.Helper()
		require.NoError(t, vault.Set(ctx, "QmPrivate", linkcreds.Environment, linkcreds.DeployKey(repository), []byte(key)))
		require.Eventually(t, func() bool {
			got, err := vault.Secret(ctx, "QmPrivate", linkcreds.Environment, linkcreds.DeployKey(repository))
			return err == nil && got == key
		}, 10*time.Second, 10*time.Millisecond)
	}

	// Nothing stored: cloned with no key, and refused.
	_, _, err = clone.Checkout(ctx, repository, "main", commit)
	require.Error(t, err, "a private repository with no deploy key stored is not readable")
	require.Zero(t, host.Served())

	// Another key stored under the name: refused by the host.
	stranger, _ := gittest.KeyPair(t)
	stored(stranger)
	_, _, err = clone.Checkout(ctx, repository, "main", commit)
	require.Error(t, err)
	require.Zero(t, host.Served(), "a key the host does not admit reads nothing")

	// The link's key: a real checkout at the pushed commit.
	stored(deployKey)
	dir, done, err := clone.Checkout(ctx, repository, "main", commit)
	require.NoError(t, err)
	defer done()
	body, err := os.ReadFile(filepath.Join(dir, "project.yaml"))
	require.NoError(t, err)
	require.Equal(t, "id: QmPrivate\n", string(body))
	require.Equal(t, 1, host.Served())
}
