package deploy_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/build/backend"
	"github.com/taubyte/tau/services/build/deploy"
)

// repository stands up a real repository with two commits, so the clone is shown
// to read the commit, not the head.

func repository(t *testing.T) (path string, first, second string) {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	path = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = path
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_AUTHOR_DATE=2020-01-01T00:00:00Z",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_COMMITTER_DATE=2020-01-01T00:00:00Z",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}

	run("init", "-q", "-b", "main")

	// The first commit is the vocabulary's own project.
	copyInto(t, basicFixture, path)
	run("add", "-A")
	run("commit", "-q", "-m", "the project")
	first = run("rev-parse", "HEAD")

	// The second changes it, and is what the branch points at afterwards.
	require.NoError(t, os.WriteFile(filepath.Join(path, "zone", "example_com.yaml"),
		[]byte("id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nfqdn: moved.example\n"), 0o644))
	run("add", "-A")
	run("commit", "-q", "-m", "moved")
	second = run("rev-parse", "HEAD")

	return path, first, second
}

func TestACloneReadsTheCommitAndNotTheBranch(t *testing.T) {
	path, first, second := repository(t)
	require.NotEqual(t, first, second)

	clone := deploy.NewClone(nil, deploy.WithAddress(func(string, forge.Protocol) (forge.Address, error) { return forge.Address{URL: path}, nil }))

	for name, want := range map[string]string{first: "example.com", second: "moved.example"} {
		dir, done, err := clone.Checkout(t.Context(), "local/repo", "main", name)
		require.NoError(t, err)

		zone, err := os.ReadFile(filepath.Join(dir, "zone", "example_com.yaml"))
		require.NoError(t, err)
		require.Contains(t, string(zone), want)

		// AND IT LEAVES NOTHING BEHIND. A build reads a stranger's repository;
		// one left next to the node's data is how one build sees another's.
		done()
		_, err = os.Stat(dir)
		require.True(t, os.IsNotExist(err), "the checkout outlived its release")
	}
}

func TestACommitThatIsNotThere(t *testing.T) {
	path, _, _ := repository(t)
	clone := deploy.NewClone(nil, deploy.WithAddress(func(string, forge.Protocol) (forge.Address, error) { return forge.Address{URL: path}, nil }))

	_, _, err := clone.Checkout(t.Context(), "local/repo", "main",
		"1111111111111111111111111111111111111111")
	require.ErrorContains(t, err, "rewritten")

	_, _, err = clone.Checkout(t.Context(), "local/repo", "main", "")
	require.ErrorContains(t, err, "names no commit")

	_, _, err = clone.Checkout(t.Context(), "local/repo", "main",
		"0000000000000000000000000000000000000000")
	require.ErrorContains(t, err, "not a commit")
}

func TestAPushOfARealRepositoryDeploys(t *testing.T) {
	path, first, second := repository(t)
	r := registry(t)

	clone := deploy.NewClone(nil, deploy.WithAddress(func(string, forge.Protocol) (forge.Address, error) { return forge.Address{URL: path}, nil }))
	d, err := deploy.New(
		deploy.WithRegions(schema.WithRegion("region-1")),
		deploy.WithProjects(projects{config: "github.com/acme/shop-config"}),
		deploy.WithCheckout(clone),
		deploy.WithLocalRegister(r),
		deploy.WithDomains(provenAll()),
		deploy.WithCloudFqdn("acme.example"),
	)
	require.NoError(t, err)

	var log out
	w := work()
	w.CommitSHA = first
	require.NoError(t, d.Run(t.Context(), w, &log))

	project, environment, err := r.ResolveHost(t.Context(), "shop.example.com")
	require.NoError(t, err)
	require.Equal(t, shopID, project)
	require.Equal(t, "prod", environment)

	// The second commit moved the zone, so the deployment answers on a
	// different name — and the revision moved with it.
	w.CommitSHA = second
	require.NoError(t, d.Run(t.Context(), w, &log))

	pointer, err := r.Resolve(t.Context(), shopID, "prod")
	require.NoError(t, err)
	require.Equal(t, uint64(2), pointer.Revision)

	_, _, err = r.ResolveHost(t.Context(), "shop.moved.example")
	require.NoError(t, err)
}

func TestHowACloudReachesARepository(t *testing.T) {
	address, err := deploy.HTTPS("github.com/acme/shop", forge.HTTPS)
	require.NoError(t, err)
	require.Equal(t, forge.Address{URL: "https://github.com/acme/shop.git"}, address)
	_, err = deploy.HTTPS("", forge.HTTPS)
	require.Error(t, err)
	_, err = deploy.HTTPS("github.com/acme/shop", forge.SSH)
	require.ErrorContains(t, err, "no forge is bound", "a bare https address has no ssh answer to give")

	f := fake.New(fake.GitHubLike)
	forges, err := f.Registry(forge.Policy{})
	require.NoError(t, err)
	forged := deploy.Forged(forges)

	address, err = forged(f.Keys().Writable.String(), forge.SSH)
	require.NoError(t, err)
	require.NotEmpty(t, address.SSHAccount, "an ssh address says which account a key authenticates as")
	require.NotEmpty(t, address.URL)

	anonymous, err := forged(f.Keys().Writable.String(), forge.HTTPS)
	require.NoError(t, err)
	require.Empty(t, anonymous.SSHAccount, "an https address authenticates as nobody")
	require.True(t, strings.HasPrefix(anonymous.URL, "https://"), "the forge's own https clone url, got %q", anonymous.URL)

	_, err = forged("codeberg.org/acme/shop", forge.SSH)
	var none *forge.NoDriverError
	require.ErrorAs(t, err, &none, "a host no forge is bound to is not reached by guessing")

	_, err = forged("", forge.HTTPS)
	require.ErrorContains(t, err, "not a repository")
}

// keys stands in for auth's per-repository deploy keys.
type keys struct {
	key string
	err error
}

func (k keys) DeployKey(context.Context, string) (string, error) { return k.key, k.err }

func TestReadingAPrivateRepository(t *testing.T) {
	path, first, _ := repository(t)
	local := deploy.WithAddress(func(string, forge.Protocol) (forge.Address, error) {
		return forge.Address{URL: path, SSHAccount: "git"}, nil
	})

	// A repository that needs no key: auth says so with an empty string and no
	// error, and the clone proceeds unauthenticated.
	dir, done, err := deploy.NewClone(keys{}, local).Checkout(t.Context(), "local/repo", "main", first)
	require.NoError(t, err)
	require.NotEmpty(t, dir)
	done()

	// AUTH FAILING IS NOT A PUBLIC REPOSITORY. Falling back to an
	// unauthenticated clone would turn an outage into a build that cannot see
	// the code and says the repository is empty.
	_, _, err = deploy.NewClone(keys{err: errors.New("auth is down")}, local).
		Checkout(t.Context(), "local/repo", "main", first)
	require.ErrorContains(t, err, "auth is down")
	require.ErrorContains(t, err, "local/repo")

	// A key that is not one fails at the clone rather than being ignored.
	pinned := deploy.WithHostKeys(func(host string) []gossh.PublicKey {
		require.Equal(t, "local", host, "keys are asked for by the host the repository key names")
		return []gossh.PublicKey{hostPublicKey(t)}
	})
	_, _, err = deploy.NewClone(keys{key: "not a pem"}, local, pinned).
		Checkout(t.Context(), "local/repo", "main", first)
	require.ErrorContains(t, err, "parsing private key")

	// AN SSH CLONE TO A HOST NOTHING PINNED IS REFUSED, before git is asked: a
	// deploy key proves who is cloning, and only the host key proves who answered.
	_, _, err = deploy.NewClone(keys{key: "any"}, local).
		Checkout(t.Context(), "local/repo", "main", first)
	require.ErrorContains(t, err, "no ssh host key is pinned for local/repo")

	// A KEY OVER HTTPS AUTHENTICATES NOTHING, so it is refused by name rather
	// than cloned anonymously and failed as a missing repository.
	overHTTPS := deploy.WithAddress(func(string, forge.Protocol) (forge.Address, error) {
		return forge.Address{URL: path}, nil
	})
	_, _, err = deploy.NewClone(keys{key: "any"}, overHTTPS).
		Checkout(t.Context(), "local/repo", "main", first)
	require.ErrorContains(t, err, "which is not ssh")
}

func TestAKeyThatIsNotARepository(t *testing.T) {
	_, _, err := deploy.NewClone(nil).Checkout(t.Context(), "", "main", "abc")
	require.ErrorContains(t, err, "not a repository")
}

func hostPublicKey(t *testing.T) gossh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	key, err := gossh.NewPublicKey(pub)
	require.NoError(t, err)
	return key
}

func TestAPushIsConfirmedAtTheRepository(t *testing.T) {
	path, first, second := repository(t)
	clone := deploy.NewClone(nil, deploy.WithAddress(func(string, forge.Protocol) (forge.Address, error) { return forge.Address{URL: path}, nil }))

	require.NoError(t, clone.Confirm(t.Context(), "local/repo", "refs/heads/main", second))
	require.ErrorIs(t, clone.Confirm(t.Context(), "local/repo", "refs/heads/main", first), backend.ErrNotAtCommit,
		"a superseded commit is not the ref")
	require.ErrorIs(t, clone.Confirm(t.Context(), "local/repo", "refs/heads/gone", second), backend.ErrNotAtCommit)

	err := clone.Confirm(t.Context(), "", "refs/heads/main", second)
	require.ErrorContains(t, err, "not a repository")
	err = deploy.NewClone(keys{err: errors.New("auth is down")}).Confirm(t.Context(), "local/repo", "refs/heads/main", second)
	require.ErrorContains(t, err, "auth is down")

	missing := deploy.NewClone(nil, deploy.WithAddress(func(string, forge.Protocol) (forge.Address, error) {
		return forge.Address{URL: filepath.Join(t.TempDir(), "nothing")}, nil
	}))
	err = missing.Confirm(t.Context(), "local/repo", "refs/heads/main", second)
	require.ErrorContains(t, err, "listing local/repo")
	require.NotErrorIs(t, err, backend.ErrNotAtCommit)
}
