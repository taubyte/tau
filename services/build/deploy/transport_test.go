package deploy_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/gittest"
	"github.com/taubyte/tau/services/build/deploy"
)

// The credential decides the transport: a keyed repository over ssh against a
// pinned host, a keyless one over anonymous https, and a failed clone never
// retried another way.

// asked records which protocol each clone asked for an address over.
type asked struct {
	mu   sync.Mutex
	over []forge.Protocol
}

func (a *asked) address(ssh, https forge.Address) deploy.CloneOption {
	return deploy.WithAddress(func(_ string, over forge.Protocol) (forge.Address, error) {
		a.mu.Lock()
		a.over = append(a.over, over)
		a.mu.Unlock()
		if over == forge.SSH {
			return ssh, nil
		}
		return https, nil
	})
}

func (a *asked) protocols() []forge.Protocol {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]forge.Protocol(nil), a.over...)
}

func TestTheCredentialDecidesTheTransport(t *testing.T) {
	private, public := gittest.KeyPair(t)
	server := gittest.NewServer(t, func(k gossh.PublicKey) bool {
		return string(k.Marshal()) == string(public.Marshal())
	})
	st, commit := gittest.Repository(t, map[string]string{"README": "over ssh\n"})
	server.Serve("/acme/pay.git", st)
	pinned := deploy.WithHostKeys(func(string) []gossh.PublicKey { return []gossh.PublicKey{server.HostKey} })
	sshAddress := forge.Address{URL: server.URL("/acme/pay.git"), SSHAccount: "git"}

	// A KEYED REPOSITORY IS READ OVER SSH, with its key, end to end.
	var keyed asked
	dir, done, err := deploy.NewClone(keys{key: private}, keyed.address(sshAddress, forge.Address{URL: "https://unused.invalid/acme/pay.git"}), pinned).
		Checkout(t.Context(), server.Addr+"/acme/pay", "main", commit)
	require.NoError(t, err)
	body, err := os.ReadFile(filepath.Join(dir, "README"))
	require.NoError(t, err)
	require.Equal(t, "over ssh\n", string(body))
	done()
	require.Equal(t, []forge.Protocol{forge.SSH}, keyed.protocols(), "a deploy key is an ssh key: https is never asked for")
	require.Equal(t, 1, server.Served())

	// A KEYLESS REPOSITORY IS READ OVER HTTPS, anonymously: the address asked
	// for is the https one, and the ssh host is never dialled.
	path, first, _ := repository(t)
	var keyless asked
	_, done, err = deploy.NewClone(keys{}, keyless.address(sshAddress, forge.Address{URL: path})).
		Checkout(t.Context(), server.Addr+"/acme/pay", "main", first)
	require.NoError(t, err)
	done()
	require.Equal(t, []forge.Protocol{forge.HTTPS}, keyless.protocols())
	require.Equal(t, 1, server.Served(), "a keyless clone never reaches the ssh host")

	// So is one with no credentials store at all.
	var none asked
	_, done, err = deploy.NewClone(nil, none.address(sshAddress, forge.Address{URL: path})).
		Checkout(t.Context(), "local/repo", "main", first)
	require.NoError(t, err)
	done()
	require.Equal(t, []forge.Protocol{forge.HTTPS}, none.protocols())
}

func TestAWrongKeyDoesNotFallBack(t *testing.T) {
	_, public := gittest.KeyPair(t)
	wrong, _ := gittest.KeyPair(t)
	server := gittest.NewServer(t, func(k gossh.PublicKey) bool {
		return string(k.Marshal()) == string(public.Marshal())
	})
	st, commit := gittest.Repository(t, map[string]string{"README": "secret\n"})
	server.Serve("/acme/pay.git", st)

	var a asked
	_, _, err := deploy.NewClone(keys{key: wrong},
		a.address(forge.Address{URL: server.URL("/acme/pay.git"), SSHAccount: "git"}, forge.Address{URL: server.URL("/acme/pay.git")}),
		deploy.WithHostKeys(func(string) []gossh.PublicKey { return []gossh.PublicKey{server.HostKey} })).
		Checkout(t.Context(), server.Addr+"/acme/pay", "main", commit)
	require.ErrorContains(t, err, "unable to authenticate")
	require.NotContains(t, err.Error(), "tau link", "a keyed clone that fails is not a missing link")
	require.Equal(t, 0, server.Served())
	require.Equal(t, []forge.Protocol{forge.SSH}, a.protocols(), "the https address is never asked for after ssh failed")
}

func TestAKeylessPrivateRepositoryIsNamed(t *testing.T) {
	var hits int
	var mu sync.Mutex
	forgeHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(forgeHTTP.Close)

	var a asked
	_, _, err := deploy.NewClone(keys{}, a.address(forge.Address{}, forge.Address{URL: forgeHTTP.URL + "/acme/pay.git"})).
		Checkout(t.Context(), "github.com/acme/pay", "main", "0123456789abcdef0123456789abcdef01234567")
	require.ErrorContains(t, err, "github.com/acme/pay was not found or is not publicly readable; if it is private, run tau link")
	require.Equal(t, []forge.Protocol{forge.HTTPS}, a.protocols(), "no ssh address is asked for without a key")
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, hits, "one anonymous attempt, never a second")

	// A 404 is a missing repository or a hidden private one; over https the
	// two look alike, so the message names both.
	missing := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(missing.Close)
	_, _, err = deploy.NewClone(keys{}, a.address(forge.Address{}, forge.Address{URL: missing.URL + "/acme/gone.git"})).
		Checkout(t.Context(), "github.com/acme/gone", "main", "0123456789abcdef0123456789abcdef01234567")
	require.ErrorContains(t, err, "github.com/acme/gone was not found or is not publicly readable; if it is private, run tau link")

	// A failure that is not a refusal — a forge that is down — is passed
	// through as it is.
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	_, _, err = deploy.NewClone(keys{}, a.address(forge.Address{}, forge.Address{URL: down.URL + "/acme/pay.git"})).
		Checkout(t.Context(), "github.com/acme/pay", "main", "0123456789abcdef0123456789abcdef01234567")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "tau link")
}
