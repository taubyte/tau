package bitbucket_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/bitbucket"
	"github.com/taubyte/tau/pkg/git/repokey"
)

func stubServer(t *testing.T, h http.Handler) (string, bitbucket.Option) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "https://"), bitbucket.Transport(srv.Client().Transport)
}

func registryOn(t *testing.T, h http.Handler, opts ...bitbucket.Option) (*forge.Registry, string) {
	t.Helper()
	host, tr := stubServer(t, h)
	drv, err := bitbucket.New(host, append([]bitbucket.Option{tr}, opts...)...)
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)
	return reg, host
}

func sessionOn(t *testing.T, h http.Handler, full string, opts ...bitbucket.Option) (*forge.Session, repokey.Key) {
	t.Helper()
	reg, host := registryOn(t, h, opts...)
	k, err := repokey.Parse("https://" + host + "/" + full)
	require.NoError(t, err)
	sess, err := reg.For(k, forge.Token(goodToken))
	require.NoError(t, err)
	return sess, k
}

func answer(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	})
}

func ctx() context.Context { return context.Background() }

func TestNewRefusesAHostNoKeyWillEverMatch(t *testing.T) {
	for _, host := range []string{
		"",
		"Bitbucket.example",         // a key's host is lowercased
		"https://bitbucket.example", // a host is not a URL
		"bitbucket.example:443",     // the default port is dropped from a key
		"bitbucket.example/",        // a trailing slash is punctuation, not a host
		"bitbucket.example/acme",    // that is a path
	} {
		t.Run(fmt.Sprintf("%q", host), func(t *testing.T) {
			_, err := bitbucket.New(host)
			require.Error(t, err, "a driver bound to a host no key resolves to would be registered and unreachable forever")
		})
	}
}

func TestTransportNeedsARoundTripper(t *testing.T) {
	_, err := bitbucket.New("bitbucket.example", bitbucket.Transport(nil))
	require.Error(t, err)
}

func TestAddressableRefusesWhatBitbucketCannotAddress(t *testing.T) {
	drv, err := bitbucket.New("bitbucket.example")
	require.NoError(t, err)

	for name, spelling := range map[string]string{
		"a nested owner":      "https://bitbucket.example/acme/team/widget",
		"no owner at all":     "https://bitbucket.example/widget",
		"a key on other host": "https://gitlab.com/acme/widget",
	} {
		t.Run(name, func(t *testing.T) {
			k, err := repokey.Parse(spelling)
			require.NoError(t, err)
			require.Error(t, drv.Addressable(k))
		})
	}

	k, err := repokey.Parse("https://bitbucket.example/acme/widget")
	require.NoError(t, err)
	require.NoError(t, drv.Addressable(k))
}

func TestBindRefusesACredentialNobodySet(t *testing.T) {
	drv, err := bitbucket.New("bitbucket.example")
	require.NoError(t, err)
	_, err = drv.Bind(forge.Credential{})
	require.ErrorIs(t, err, forge.ErrNoCredential, "an unset credential must never degrade into anonymity")
}

func TestTheCredentialNeverReachesAnErrorString(t *testing.T) {
	for name, h := range map[string]http.Handler{
		"401": answer(http.StatusUnauthorized, `{}`),
		"403": answer(http.StatusForbidden, `{}`),
		"500 echoing the request": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, "your Authorization was: "+r.Header.Get("Authorization"))
		}),
		"a redirect off the host": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://elsewhere.example.invalid/x", http.StatusFound)
		}),
		"garbage 200": answer(http.StatusOK, "\x00 not json"),
	} {
		t.Run(name, func(t *testing.T) {
			sess, key := sessionOn(t, h, "acme/pay")
			callback, err := url.Parse("https://build.example.invalid/hook")
			require.NoError(t, err)
			secret, err := forge.NewSecret()
			require.NoError(t, err)

			errs := map[string]error{}
			_, err = sess.Whoami(ctx())
			errs["Whoami"] = err
			_, err = sess.Access(ctx(), key)
			errs["Access"] = err
			_, err = sess.Repository(ctx(), key)
			errs["Repository"] = err
			_, err = sess.Create(ctx(), key, forge.CreateOptions{})
			errs["Create"] = err
			errs["Delete"] = sess.Delete(ctx(), key)
			_, err = sess.InstallDeployKey(ctx(), key, "tau", "ssh-ed25519 AAAA")
			errs["InstallDeployKey"] = err
			errs["RemoveDeployKey"] = sess.RemoveDeployKey(ctx(), key, "7")
			_, err = sess.RegisterPushHook(ctx(), key, callback, secret)
			errs["RegisterPushHook"] = err
			errs["RemovePushHook"] = sess.RemovePushHook(ctx(), key, "7")
			errs["ListRepositories"] = sess.ListRepositories(ctx(), func(forge.Repository) bool { return true })

			for method, err := range errs {
				if err == nil {
					continue
				}
				for _, rendering := range []string{err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err)} {
					assert.NotContains(t, rendering, goodToken, "%s leaked the credential", method)
				}
			}
		})
	}
}
