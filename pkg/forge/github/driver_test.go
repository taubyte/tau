package github_test

// What the conformance suite cannot reach: the branches a correct forge never
// takes. A forge that answers a repository with a clone URL naming another
// repository, one that 302s a credentialed request off its own host, one that
// returns an id of zero for a hook nothing could then remove. Each is a thing
// this driver has a line for, and a line with no test is a line nobody has run.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/github"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// ─────────────────────────────── helpers ───────────────────────────────

func stubServer(t *testing.T, h http.Handler) (string, github.Option) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "https://"), github.Transport(srv.Client().Transport)
}

func registryOn(t *testing.T, h http.Handler, opts ...github.Option) (*forge.Registry, string) {
	t.Helper()
	host, tr := stubServer(t, h)
	drv, err := github.New(host, append([]github.Option{tr}, opts...)...)
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)
	return reg, host
}

func sessionOn(t *testing.T, h http.Handler, full string, opts ...github.Option) (*forge.Session, repokey.Key) {
	t.Helper()
	reg, host := registryOn(t, h, opts...)
	k, err := repokey.Parse("https://" + host + "/" + full)
	require.NoError(t, err)
	sess, err := reg.For(k, forge.Token(goodToken))
	require.NoError(t, err)
	return sess, k
}

// answer is a server that gives one status and one body to everything.
func answer(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	})
}

func ctx() context.Context { return context.Background() }

// ─────────────────────────────── construction ───────────────────────────────

func TestNewRefusesAHostNoKeyWillEverMatch(t *testing.T) {
	for _, host := range []string{
		"",
		"GitHub.com",         // a key's host is lowercased
		"https://github.com", // a host is not a URL
		"github.com:443",     // the default port is dropped from a key
		"github.com/",        // a trailing slash is punctuation, not a host
		"github.com/acme",    // that is a path
	} {
		t.Run(fmt.Sprintf("%q", host), func(t *testing.T) {
			_, err := github.New(host)
			require.Error(t, err, "a driver bound to a host no key resolves to would be registered and unreachable forever")
		})
	}
}

// canned answers every request without dialing anything, so what a driver would
// have SENT can be asserted without a server pretending to be github.com.
type canned struct {
	mu   sync.Mutex
	sent []*http.Request
	body string
}

func (c *canned) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.sent = append(c.sent, r)
	c.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(c.body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

// hops records what the driver actually put on the wire, UNDER the pin, so a
// request that was replayed at a second address is a second entry.
type hops struct {
	mu   sync.Mutex
	urls []string
	base http.RoundTripper
}

func (h *hops) RoundTrip(r *http.Request) (*http.Response, error) {
	h.mu.Lock()
	h.urls = append(h.urls, r.URL.String())
	h.mu.Unlock()
	return h.base.RoundTrip(r)
}

// TestDotComTalksToTheAPIHostAndNotToTheRepositoryHost: github.com serves its
// API from api.github.com, so the host a key names and the host a request goes
// to are two different values — and the credential is pinned to the second.
func TestDotComTalksToTheAPIHostAndNotToTheRepositoryHost(t *testing.T) {
	c := &canned{body: `{"login":"octo-tau","id":7}`}
	drv, err := github.New("github.com", github.Transport(c))
	require.NoError(t, err)
	assert.Equal(t, "github.com", drv.Host(), "the driver answers for the host a key names")

	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: "github.com", Driver: drv})
	require.NoError(t, err)
	sess, err := reg.Host("github.com", forge.Token(goodToken))
	require.NoError(t, err)

	who, err := sess.Whoami(ctx())
	require.NoError(t, err)
	assert.Equal(t, "octo-tau", who.Login)
	assert.Equal(t, "7", who.Subject, "the subject is the forge's user number, rendered once and never parsed again")

	require.Len(t, c.sent, 1)
	assert.Equal(t, "https://api.github.com/user", c.sent[0].URL.String())
	assert.Equal(t, "Bearer "+goodToken, c.sent[0].Header.Get("Authorization"))
	assert.Equal(t, "2022-11-28", c.sent[0].Header.Get("X-GitHub-Api-Version"))
}

// TestAnEnterpriseInstanceServesItsAPIFromItsOwnHost is the other half: no
// enum, no provider field, just a different host in the binding.
func TestAnEnterpriseInstanceServesItsAPIFromItsOwnHost(t *testing.T) {
	c := &canned{body: `{"login":"octo-tau","id":7}`}
	drv, err := github.New("github.acme.example", github.Transport(c))
	require.NoError(t, err)

	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: "github.acme.example", Driver: drv})
	require.NoError(t, err)
	sess, err := reg.Host("github.acme.example", forge.Token(goodToken))
	require.NoError(t, err)
	_, err = sess.Whoami(ctx())
	require.NoError(t, err)

	require.Len(t, c.sent, 1)
	assert.Equal(t, "https://github.acme.example/api/v3/user", c.sent[0].URL.String())
}

func TestTransportNeedsARoundTripper(t *testing.T) {
	_, err := github.New("github.com", github.Transport(nil))
	require.Error(t, err)
}

func TestAppRefusesACredentialItCannotUse(t *testing.T) {
	_, err := github.New("github.com", github.App("", testKeyPEM()))
	require.Error(t, err, "an app credential with no client id cannot sign an assertion anybody would accept")

	_, err = github.New("github.com", github.App(appClientID, []byte("-----BEGIN RSA PRIVATE KEY-----\nnope\n-----END RSA PRIVATE KEY-----")))
	require.Error(t, err, "an unusable key must fail here, not at whichever registration first needs it")
	assert.NotContains(t, err.Error(), "nope", "the key material is in the error")
}

// TestAddressableRefusesWhatGitHubCannotAddress. The nested case is the one the
// suite covers; these two are the others, and Addressable is exported so they
// are reachable.
func TestAddressableRefusesWhatGitHubCannotAddress(t *testing.T) {
	drv, err := github.New("github.com")
	require.NoError(t, err)

	for name, spelling := range map[string]string{
		"a nested owner":      "https://github.com/acme/team/widget",
		"no owner at all":     "https://github.com/widget",
		"a key on other host": "https://gitlab.com/acme/widget",
	} {
		t.Run(name, func(t *testing.T) {
			k, err := repokey.Parse(spelling)
			require.NoError(t, err)
			require.Error(t, drv.Addressable(k))
		})
	}

	k, err := repokey.Parse("https://github.com/acme/widget")
	require.NoError(t, err)
	require.NoError(t, drv.Addressable(k))
}

func TestBindRefusesACredentialNobodySet(t *testing.T) {
	drv, err := github.New("github.com")
	require.NoError(t, err)
	_, err = drv.Bind(forge.Credential{})
	require.ErrorIs(t, err, forge.ErrNoCredential, "an unset credential must never degrade into anonymity")
}

// ─────────────────────────── the credential never leaves ───────────────────────────

// TestTheCredentialNeverReachesAnErrorString drives every method against a
// host that fails in every way and reads the whole error — Error(), %v and
// %+v — for the token. An error string is the most-copied thing in a log, and
// a host that gets to choose what lands in one would choose to put the
// caller's own credential there.
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

// TestNoRedirectIsFollowed. An http.Client replays a request wherever a 3xx
// points, and GitHub answers a renamed or TRANSFERRED repository with 301 to
// its current path — so a request built for one repository comes back answered
// by another, carrying that other repository's `permissions`.
//
// PinnedTransport is not the answer to that and cannot be. It refuses the hop
// that leaves the host, which is the whole of what a per-hop host check can
// decide; a redirect to another repository on the SAME host is one it permits
// by design. So the driver refuses to follow at all, and this asserts both
// halves: for the off-host redirect the error is no longer the pin's, because
// the driver never got as far as asking the pin to make a second request.
func TestNoRedirectIsFollowed(t *testing.T) {
	for name, target := range map[string]string{
		"off the host, which the pin would have refused": "https://elsewhere.example.invalid/api/v3/user",
		"onto another repository, which the pin permits": "/api/v3/repos/attacker/pay",
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/repos/acme/pay") {
					http.Redirect(w, r, target, http.StatusFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"full_name":"attacker/pay","clone_url":"https://`+r.Host+`/attacker/pay.git",
					"default_branch":"main","permissions":{"admin":true,"push":true,"pull":true}}`)
			}))
			t.Cleanup(srv.Close)
			host := strings.TrimPrefix(srv.URL, "https://")
			seen := &hops{base: srv.Client().Transport}

			drv, err := github.New(host, github.Transport(seen))
			require.NoError(t, err)
			reg, err := forge.New(forge.Policy{}, forge.Binding{Host: host, Driver: drv})
			require.NoError(t, err)
			key, err := repokey.Parse("https://" + host + "/acme/pay")
			require.NoError(t, err)
			sess, err := reg.For(key, forge.Token(goodToken))
			require.NoError(t, err)

			lvl, err := sess.Access(ctx(), key)
			require.Error(t, err, "the redirect was followed and another repository's grant returned as this one's")
			assert.Equal(t, forge.AccessNone, lvl)
			assert.Len(t, seen.urls, 1, "the request was replayed at what the forge pointed at: %v", seen.urls)
			// "answered " and not the bare number, for the reason the gitlab
			// driver's redirect table gives: a needle that is only digits is a
			// needle any other number in the sentence can satisfy. This
			// driver's own rendering carries no host, so nothing here satisfies
			// it today — the anchor is what keeps that true.
			assert.Contains(t, err.Error(), "answered 302", "the status is the fact an operator needs")

			var leak *forge.CredentialLeakError
			assert.False(t, errors.As(err, &leak),
				"the driver asked the pin to make the second request instead of refusing to make one")
			assert.NotContains(t, err.Error(), goodToken)
		})
	}
}

// TestAccessIsBoundToTheKeyItWasAskedAbout is the whole defect in one call.
// GitHub answers a request for a renamed or TRANSFERRED repository with 301 to
// its current path; what comes back is a repository object and the caller's
// permissions ON THAT OTHER REPOSITORY. Access is what the registration gate is
// built on, and the name it was asked about is free for anyone to re-register
// the moment the transfer happens.
func TestAccessIsBoundToTheKeyItWasAskedAbout(t *testing.T) {
	var asked []string
	sess, key := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/repos/acme/pay"):
			http.Redirect(w, r, "/api/v3/repos/attacker/pay", http.StatusMovedPermanently)
		case strings.HasSuffix(r.URL.Path, "/repos/attacker/pay"):
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"full_name":"attacker/pay",
				"clone_url":"https://`+r.Host+`/attacker/pay.git",
				"ssh_url":"ssh://git@`+r.Host+`/attacker/pay.git",
				"default_branch":"main","private":false,
				"permissions":{"admin":true,"push":true,"pull":true}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}), "acme/pay")

	got, err := sess.Access(ctx(), key)
	t.Logf("requests: %v", asked)
	t.Logf("Access(%s) = %v, err = %v", key, got, err)

	assert.False(t, got.CanWrite(),
		"Access answered %v for %q on the strength of the caller's grant on attacker/pay; "+
			"the answer is not bound to the key that was asked about", got, key)
}

// TestAWriteIsNotReplayedAtWhateverARedirectNames is the same defect on the
// write side, and the one no check-afterwards can undo. GitHub answers a POST
// to a moved repository with 307, which Go replays method and body against, so
// the hook is created on the repository the redirect names while the forge.Hook
// that comes back is stamped with the key the CALLER asked for. On 301 it is
// worse for a delete: Go rewrites a non-HEAD request to GET, so Delete would
// have read a 200 and reported success having deleted nothing.
func TestAWriteIsNotReplayedAtWhateverARedirectNames(t *testing.T) {
	var created string
	sess, key := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/repos/acme/pay/hooks"):
			http.Redirect(w, r, "/api/v3/repos/attacker/pay/hooks", http.StatusTemporaryRedirect)
		case strings.HasSuffix(r.URL.Path, "/repos/attacker/pay/hooks"):
			created = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":991}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}), "acme/pay")

	callback, err := url.Parse("https://build.example.invalid/hook")
	require.NoError(t, err)
	secret, err := forge.NewSecret()
	require.NoError(t, err)

	hook, err := sess.RegisterPushHook(ctx(), key, callback, secret)
	t.Logf("hook = {Key:%s Handle:%s Scheme:%s}, err = %v, server created it at %q", hook.Key, hook.Handle, hook.Scheme, err, created)

	assert.Empty(t, created, "a hook recorded under %q was installed on attacker/pay", key)
	require.Error(t, err)
}

// TestAnAnswerAboutAnotherRepositoryIsRefused is what no redirect rule can see:
// a host that answers 200 at the path this driver built with a body describing
// a DIFFERENT repository. A proxy told the wrong external URL does it by
// accident; a forge handing a caller somebody else's grant does it on purpose.
func TestAnAnswerAboutAnotherRepositoryIsRefused(t *testing.T) {
	sess, key := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"full_name":"attacker/pay","clone_url":"https://`+r.Host+`/attacker/pay.git",
			"default_branch":"main","permissions":{"admin":true,"push":true,"pull":true}}`)
	}), "acme/pay")

	lvl, err := sess.Access(ctx(), key)
	require.Error(t, err, "the grant on attacker/pay was returned as the grant on acme/pay")
	assert.Equal(t, forge.AccessNone, lvl)

	// Repository is the other half of the asymmetry and is asserted here so the
	// split cannot be lost by accident: it REPORTS what the forge named, which
	// is how a rename reaches a consumer at all.
	repo, err := sess.Repository(ctx(), key)
	require.NoError(t, err)
	assert.Equal(t, "attacker", repo.Key.Owner, "the repository was filed under the name that was asked, not the one the forge named")
}

// ─────────────────────────────── access ───────────────────────────────

func TestAccessMapsGitHubsRolesOntoTausRungs(t *testing.T) {
	for name, tc := range map[string]struct {
		perms string
		want  forge.Access
	}{
		"admin":    {`{"admin":true,"maintain":true,"push":true,"pull":true}`, forge.AccessAdmin},
		"maintain": {`{"maintain":true,"push":true,"pull":true}`, forge.AccessWrite},
		// GitHub sends push:true with maintain, so the row above is the one a
		// real forge produces and this one is the branch the driver keeps for
		// itself: maintain|push|admin is v1's write predicate, preserved
		// exactly, and dropping Maintain from it left the tree at exit 0
		// because every other row still had push.
		"maintain without push, which only this driver decides": {`{"maintain":true,"pull":true}`, forge.AccessWrite},
		"push":                         {`{"push":true,"pull":true}`, forge.AccessWrite},
		"pull only":                    {`{"pull":true}`, forge.AccessRead},
		"triage, which grants no push": {`{"pull":true,"triage":true}`, forge.AccessRead},
		"nothing at all":               {`{}`, forge.AccessNone},
	} {
		t.Run(name, func(t *testing.T) {
			// The clone URL names the repository that was asked about, on the
			// host it was asked of: an answer that names another repository is
			// refused now, and it is refused wherever it comes from.
			sess, key := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"full_name":"acme/pay","default_branch":"main","permissions":`+tc.perms+
					`,"clone_url":"https://`+r.Host+`/acme/pay.git"}`)
			}), "acme/pay")
			got, err := sess.Access(ctx(), key)
			require.NoError(t, err, "a definitive answer is a value, including AccessNone")
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestAForbiddenAnswerIsNotARefusal. On GitHub a 403 is a rate limit, an
// organization that blocked the OAuth app, or SAML enforcement — an operator
// problem. Reading it as "you may not push" denies a legitimate writer for a
// setting they do not control.
func TestAForbiddenAnswerIsNotARefusal(t *testing.T) {
	sess, key := sessionOn(t, answer(http.StatusForbidden, `{"message":"API rate limit exceeded"}`), "acme/pay")
	lvl, err := sess.Access(ctx(), key)
	require.Error(t, err)
	assert.Equal(t, forge.AccessNone, lvl)

	var nf *forge.NotFoundError
	assert.False(t, errors.As(err, &nf), "a rate limit read as 'no such repository'")
	var ce *forge.CredentialError
	assert.False(t, errors.As(err, &ce), "a rate limit read as a bad credential")
}

func TestARefusedCredentialIsAStatementAboutTheCredential(t *testing.T) {
	sess, key := sessionOn(t, answer(http.StatusUnauthorized, `{"message":"Bad credentials"}`), "acme/pay")
	_, err := sess.Access(ctx(), key)
	var ce *forge.CredentialError
	require.ErrorAs(t, err, &ce)
	assert.NotContains(t, ce.Error(), goodToken)
}

func TestAnAnswerThisDriverCannotReadIsAnError(t *testing.T) {
	sess, key := sessionOn(t, answer(http.StatusOK, `{"full_name": [ this is not json`), "acme/pay")
	_, err := sess.Access(ctx(), key)
	require.Error(t, err)
}

// TestARepositoryWhoseCloneURLNamesNothingIsRefused: Key is DERIVED from the
// clone URL, so a forge that answers with one this layer cannot name has not
// answered.
func TestARepositoryWhoseCloneURLNamesNothingIsRefused(t *testing.T) {
	sess, key := sessionOn(t, answer(http.StatusOK, `{"full_name":"acme/pay","clone_url":"","default_branch":"main"}`), "acme/pay")
	_, err := sess.Repository(ctx(), key)
	require.Error(t, err)
}

// TestARepositoryOnAnotherHostIsRefused. The gitlab driver refuses a clone URL
// on a host other than the one it answers for and calls it not hypothetical —
// an instance behind a proxy told the wrong external URL emits exactly that.
// The consequence here is sharper than a misfiled key: HTTPS is the address a
// builder clones with a credential, so a forge that gets to name it gets to
// name where that credential is spent, and 169.254.169.254 is a URL like any
// other.
func TestARepositoryOnAnotherHostIsRefused(t *testing.T) {
	for _, elsewhere := range []string{
		"https://evil.example/acme/pay.git",
		"http://169.254.169.254/acme/pay.git",
		"http://127.0.0.1:9/acme/pay.git",
	} {
		t.Run(elsewhere, func(t *testing.T) {
			body := `{"full_name":"acme/pay","clone_url":"` + elsewhere + `",
				"ssh_url":"ssh://git@evil.example/acme/pay.git",
				"default_branch":"main","permissions":{"pull":true}}`
			sess, key := sessionOn(t, answer(http.StatusOK, body), "acme/pay")

			repo, err := sess.Repository(ctx(), key)
			t.Logf("Repository = %+v, err = %v", repo, err)
			if err != nil {
				return
			}
			assert.Equal(t, key.Host, repo.Key.Host,
				"a repository this deployment asked %s about came back keyed to %q", key.Host, repo.Key.Host)
			assert.True(t, strings.Contains(repo.HTTPS, key.Host),
				"HTTPS clone address %q is not on the host that was asked", repo.HTTPS)
		})
	}
}

// TestARepositoryReportsBothAddressesTheForgeNamed, and reports each as
// ITSELF.
//
// HTTPS is the address a builder clones WITH A CREDENTIAL and SSH is the one it
// clones with a key, so a driver that put one where the other belongs sends a
// token to an address nothing derived a key from. No key check can see that:
// both spellings name the same repository, the key still comes from clone_url,
// and every other assertion in this package still passes. Measured before this
// test: reporting ssh_url as HTTPS left the whole tree at exit 0.
//
// The SSH URL here is on another HOST AND PORT, which is the documented
// passthrough —
// a forge may legitimately serve SSH elsewhere, nothing clones it with a bearer
// credential, and this driver reports what it was told rather than inventing
// agreement.
func TestARepositoryReportsBothAddressesTheForgeNamed(t *testing.T) {
	var https, ssh string
	sess, key := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		https = "https://" + r.Host + "/acme/pay.git"
		ssh = "ssh://git@ssh.acme.example:2222/acme/pay.git"
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"full_name":"acme/pay","clone_url":"`+https+`","ssh_url":"`+ssh+`",
			"default_branch":"main","private":true,"permissions":{"pull":true}}`)
	}), "acme/pay")

	repo, err := sess.Repository(ctx(), key)
	require.NoError(t, err)
	assert.Equal(t, key, repo.Key, "the key comes from the clone URL")
	assert.Equal(t, https, repo.HTTPS,
		"the address a clone spends a credential at is not the one the forge named for it")
	assert.Equal(t, ssh, repo.SSH,
		"an ssh address the forge serves elsewhere was rewritten or dropped")

	// And the consequence of that passthrough, stated rather than hidden: the
	// two addresses of one repository derive two keys, because the host is part
	// of the identity.
	fromSSH, err := repokey.Parse(repo.SSH)
	require.NoError(t, err)
	assert.NotEqual(t, repo.Key, fromSSH)
}

// TestAnAnswerThisDriverWillNotFinishReadingIsAnError. A response is a
// stranger's bytes even when the stranger is GitHub, and an unbounded decode is
// an unbounded allocation decided by whoever is answering. maxResponseBody is
// the whole of what stops that, and until this test it was a constant with a
// comment: deleting the LimitReader around the decode left the tree at exit 0,
// because nothing here had ever sent a large answer.
//
// The body below is valid JSON describing the repository that was asked for, so
// a driver that reads all of it SUCCEEDS — which is what makes the refusal the
// thing being asserted rather than a malformed-input path.
func TestAnAnswerThisDriverWillNotFinishReadingIsAnError(t *testing.T) {
	sess, key := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"padding":"`+strings.Repeat("x", 9<<20)+`",
			"full_name":"acme/pay","clone_url":"https://`+r.Host+`/acme/pay.git",
			"default_branch":"main","permissions":{"admin":true,"push":true,"pull":true}}`)
	}), "acme/pay")

	repo, err := sess.Repository(ctx(), key)
	require.Error(t, err, "this driver read a 9MB answer to the end and believed it: %+v", repo)
	assert.Less(t, len(err.Error()), 600, "the forge chose the length of this error")
}

func TestWhoamiRefusesACredentialBelongingToNobody(t *testing.T) {
	sess, _ := sessionOn(t, answer(http.StatusOK, `{"id":7}`), "acme/pay")
	_, err := sess.Whoami(ctx())
	require.Error(t, err, "a login-less answer is not an identity")
}

// ─────────────────────────────── listing ───────────────────────────────

// TestTheListingPagesAndSkipsWhatItCannotName. GitHub pages at 100; the second
// page is asked for because the first was full, and the walk stops on a short
// one.
func TestTheListingPagesAndSkipsWhatItCannotName(t *testing.T) {
	var pages []string
	sess, _ := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		host := r.Host
		out := make([]map[string]any, 0, 100)
		add := func(full, clone string) {
			out = append(out, map[string]any{
				"full_name": full, "clone_url": clone,
				"ssh_url": "ssh://git@" + host + "/" + full + ".git", "default_branch": "main",
			})
		}
		switch page {
		case "1":
			for i := 0; i < 98; i++ {
				full := fmt.Sprintf("acme/r%d", i)
				add(full, "https://"+host+"/"+full+".git")
			}
			// A repository this layer cannot name, and one whose shape this
			// forge cannot address. Both are skipped rather than yielded with a
			// key nothing could act on.
			add("acme/broken", "not a clone url")
			add("acme/team/widget", "https://"+host+"/acme/team/widget.git")
		case "2":
			add("acme/last", "https://"+host+"/acme/last.git")
		}
		json.NewEncoder(w).Encode(out)
	}), "acme/pay")

	var seen []repokey.Key
	require.NoError(t, sess.ListRepositories(ctx(), func(r forge.Repository) bool {
		seen = append(seen, r.Key)
		return true
	}))
	assert.Equal(t, []string{"1", "2"}, pages, "a full page must be followed by the next one")
	assert.Len(t, seen, 99, "the two repositories this driver cannot address were yielded anyway")
	assert.Equal(t, "acme/last", seen[len(seen)-1].FullName())
}

func TestTheListingStopsWhenTheCallerStops(t *testing.T) {
	sess, _ := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		out := []map[string]any{
			{"full_name": "acme/a", "clone_url": "https://" + host + "/acme/a.git", "default_branch": "main"},
			{"full_name": "acme/b", "clone_url": "https://" + host + "/acme/b.git", "default_branch": "main"},
		}
		json.NewEncoder(w).Encode(out)
	}), "acme/pay")

	calls := 0
	require.NoError(t, sess.ListRepositories(ctx(), func(forge.Repository) bool {
		calls++
		return false
	}))
	assert.Equal(t, 1, calls)
}

// TestTheListingStopsPagingEventually is the gitlab driver's row of the same
// name, which this driver did not have. Its pager terminates on a SHORT page,
// which is termination while the forge is honest; a forge that always answers a
// full one is an unbounded loop inside a call a consumer thinks is one request.
//
// THE FORGE HERE RUNS OUT EVENTUALLY, and that is not a softened case: it runs
// out well past the bound, so a driver that keeps the bound stops first and a
// driver that drops it FINISHES and returns nil. Without it the row would be
// satisfied by the test binary's own timeout, which fails no name and takes ten
// minutes to say so.
func TestTheListingStopsPagingEventually(t *testing.T) {
	const pastTheBound = 4000

	page := 0
	sess, _ := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		page++
		w.Header().Set("Content-Type", "application/json")
		if page > pastTheBound {
			io.WriteString(w, "[]")
			return
		}
		// A FULL page of entries this driver cannot name, so the loop keeps
		// going on the count — which is the whole of what termination rests on
		// — while each page stays small enough to run in milliseconds.
		io.WriteString(w, "["+strings.Repeat("{},", 99)+"{}]")
	}), "acme/pay")

	err := sess.ListRepositories(ctx(), func(forge.Repository) bool { return true })
	require.Errorf(t, err, "this driver paged a forge %d times and believed it had reached the end", page)
	assert.Contains(t, err.Error(), "refusing to keep paging")
	assert.Less(t, page, pastTheBound, "the bound is not what stopped it")
}

func TestAListingThatFailsIsAnError(t *testing.T) {
	sess, _ := sessionOn(t, answer(http.StatusUnauthorized, `{"message":"Bad credentials"}`), "acme/pay")
	err := sess.ListRepositories(ctx(), func(forge.Repository) bool { return true })
	var ce *forge.CredentialError
	require.ErrorAs(t, err, &ce)
}

// ─────────────────────────────── creation ───────────────────────────────

// TestCreatingUnderTheAuthenticatedAccountUsesTheOtherEndpoint. POST
// /orgs/{me}/repos is a 404 because a user is not an organization, so which
// endpoint applies genuinely depends on who the credential is — which is why
// Create reads the identity first.
func TestCreatingUnderTheAuthenticatedAccountUsesTheOtherEndpoint(t *testing.T) {
	var paths []string
	sess, k := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		host := r.Host
		if strings.HasSuffix(r.URL.Path, "/user") {
			io.WriteString(w, `{"login":"Octo-Tau","id":7}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"full_name": "octo-tau/personal", "default_branch": "main",
			"clone_url": "https://" + host + "/octo-tau/personal.git",
			"ssh_url":   "ssh://git@" + host + "/octo-tau/personal.git",
		})
	}), "octo-tau/personal")

	repo, err := sess.Create(ctx(), k, forge.CreateOptions{Description: "mine"})
	require.NoError(t, err)
	assert.Equal(t, k, repo.Key)
	assert.Contains(t, paths, "POST /api/v3/user/repos", "a repository under the caller's own account went to the organization endpoint")
}

// TestARepositoryCreatedSomewhereElseIsRefused. Creation is the one call that
// CHOSE where the repository would land, so a key that comes back different is
// a repository made somewhere nobody asked for — not a rename to surface.
func TestARepositoryCreatedSomewhereElseIsRefused(t *testing.T) {
	sess, key := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if strings.HasSuffix(r.URL.Path, "/user") {
			io.WriteString(w, `{"login":"octo-tau","id":7}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"full_name": "somebody/else", "default_branch": "main",
			"clone_url": "https://" + host + "/somebody/else.git",
		})
	}), "acme/fresh")

	_, err := sess.Create(ctx(), key, forge.CreateOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "created")
}

func TestCreateNeedsToKnowWhoTheCredentialIs(t *testing.T) {
	sess, key := sessionOn(t, answer(http.StatusUnauthorized, `{"message":"Bad credentials"}`), "acme/fresh")
	_, err := sess.Create(ctx(), key, forge.CreateOptions{})
	require.Error(t, err)
}

// ─────────────────────────── hooks and deploy keys ───────────────────────────

// TestAResourceWithNoIdIsRefused: a hook or a key the forge numbered zero is one
// nothing could ever remove, which is how a registrar leaks by construction.
func TestAResourceWithNoIdIsRefused(t *testing.T) {
	sess, key := sessionOn(t, answer(http.StatusCreated, `{"id":0}`), "acme/site")
	_, err := sess.InstallDeployKey(ctx(), key, "tau", "ssh-ed25519 AAAA tau")
	require.Error(t, err)

	callback, err := url.Parse("https://build.example.invalid/hook")
	require.NoError(t, err)
	secret, err := forge.NewSecret()
	require.NoError(t, err)
	_, err = sess.RegisterPushHook(ctx(), key, callback, secret)
	require.Error(t, err)
}

// TestA404OnAResourceIsNotAVerdictAboutTheRepository is the whole of what
// classifyResource exists for, and until this test it was a comment.
//
// A hook and a deploy key live UNDER a repository, and GitHub answers 404 for a
// handle that is gone exactly as it answers 404 for a repository that is. Filing
// the first as *NotFoundError says the host serves no repository acme/site — a
// repository that is plainly there — and a consumer acting on that deregisters a
// live project because a hook it was tidying up had already been removed.
// Measured before this test: routing the removals through classify(err, key)
// instead left the whole tree at exit 0.
//
// THE SPLIT IS ASSERTED IN BOTH DIRECTIONS, because the interesting half is not
// that a removal is exempt but that a fetch is not. The two POSTs are on the
// fetch side deliberately: creating a hook under a repository that 404s IS the
// repository being missing, since the resource does not exist yet to be the
// subject.
func TestA404OnAResourceIsNotAVerdictAboutTheRepository(t *testing.T) {
	sess, key := sessionOn(t, answer(http.StatusNotFound, `{"message":"Not Found"}`), "acme/site")
	callback, err := url.Parse("https://build.example.invalid/hook")
	require.NoError(t, err)
	secret, err := forge.NewSecret()
	require.NoError(t, err)

	var missing *forge.NotFoundError
	for name, err := range map[string]error{
		"removing a deploy key": sess.RemoveDeployKey(ctx(), key, "7"),
		"removing a hook":       sess.RemovePushHook(ctx(), key, "7"),
	} {
		require.Error(t, err, name)
		assert.NotErrorAsf(t, err, &missing,
			"%s answered that the host serves no repository %q, which is a repository that is plainly there", name, key)
	}

	_, err = sess.Repository(ctx(), key)
	require.ErrorAs(t, err, &missing, "a 404 on the repository itself is the one thing this taxonomy member is for")
	_, err = sess.InstallDeployKey(ctx(), key, "tau", "ssh-ed25519 AAAA")
	require.ErrorAs(t, err, &missing, "installing under a repository that is not there is that repository not being there")
	_, err = sess.RegisterPushHook(ctx(), key, callback, secret)
	require.ErrorAs(t, err, &missing)
}

// TestRemovingSomethingWithoutItsHandleIsRefused. An empty handle would build a
// path ending in a slash, which addresses the COLLECTION: a removal of one thing
// answered by all of them.
func TestRemovingSomethingWithoutItsHandleIsRefused(t *testing.T) {
	var reached int
	sess, key := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached++
		w.WriteHeader(http.StatusNoContent)
	}), "acme/site")

	require.Error(t, sess.RemoveDeployKey(ctx(), key, ""))
	require.Error(t, sess.RemovePushHook(ctx(), key, ""))
	assert.Equal(t, 0, reached, "an empty handle reached the forge")
}

// TestAHandleThatNamesSomethingElseIsRefused is the row above generalized to
// what it was always about: an empty handle is one spelling of a handle that
// does not name a resource, and the OTHERS WERE SENT.
//
// MEASURED ON THE CLEAN TREE: RemoveDeployKey(acme/site, "..") composed and SENT
//
//	DELETE /api/v3/repos/acme/site/keys/..
//
// which RFC 3986 dot-segment removal — Go's own path.Clean, and every conforming
// proxy, gateway and origin — resolves to /api/v3/repos/acme/site: GitHub's
// delete-a-repository endpoint, reached with the caller's credential, on the
// repository tau was asked to tidy. Go's own httptest server answered it with a
// 307 to exactly that path. "." resolves one rung less far, to the COLLECTION,
// which is the defect the row above is written against; "a/b" names a resource
// under a collection nobody asked about with no dot in it at all.
//
// ESCAPING IS NOT THE ANSWER TO ANY OF THESE, which is why they were sent for
// six rounds under a function whose job was thought to be escaping:
// url.PathEscape("..") is "..", because a dot is unreserved.
func TestAHandleThatNamesSomethingElseIsRefused(t *testing.T) {
	for _, handle := range []string{"..", ".", "../..", "a/b", "../../attacker?a#b"} {
		t.Run(handle, func(t *testing.T) {
			var reached int
			sess, key := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached++
				w.WriteHeader(http.StatusNoContent)
			}), "acme/site")

			assert.Error(t, sess.RemoveDeployKey(ctx(), key, forge.Handle(handle)))
			assert.Error(t, sess.RemovePushHook(ctx(), key, forge.Handle(handle)))
			assert.Equal(t, 0, reached, "a handle naming something else reached the forge")
		})
	}
}

// TestTheDeployKeyGoesOutReadOnly reads the request body, because the assertion
// the suite can make is the one the fixture reports and this one is the wire.
func TestTheDeployKeyGoesOutReadOnly(t *testing.T) {
	var body map[string]any
	sess, key := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"id":12}`)
	}), "acme/site")

	h, err := sess.InstallDeployKey(ctx(), key, "tau", "ssh-ed25519 AAAA tau")
	require.NoError(t, err)
	assert.Equal(t, forge.Handle("12"), h)
	assert.Equal(t, true, body["read_only"], "tau's deploy key exists so a builder can clone")
}

// TestTheHookSubscribesToPushesAndNothingElse, and hands over the wire string
// verbatim: re-encoding it registers a hook that looks perfect and rejects every
// delivery it will ever receive.
func TestTheHookSubscribesToPushesAndNothingElse(t *testing.T) {
	var body struct {
		Events []string `json:"events"`
		Active bool     `json:"active"`
		Config struct {
			URL         string `json:"url"`
			ContentType string `json:"content_type"`
			InsecureSSL string `json:"insecure_ssl"`
			Secret      string `json:"secret"`
		} `json:"config"`
	}
	sess, key := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"id":99}`)
	}), "acme/site")

	secret, err := forge.NewSecret()
	require.NoError(t, err)
	callback, err := url.Parse("https://build.example.invalid/hook/x")
	require.NoError(t, err)

	hook, err := sess.RegisterPushHook(ctx(), key, callback, secret)
	require.NoError(t, err)
	assert.Equal(t, forge.SchemeHubSHA256, hook.Scheme)
	assert.Equal(t, []string{"push"}, body.Events)
	assert.True(t, body.Active)
	assert.Equal(t, "json", body.Config.ContentType)
	assert.Equal(t, "0", body.Config.InsecureSSL)
	assert.Equal(t, callback.String(), body.Config.URL)

	wire, err := forge.SchemeHubSHA256.Wire(secret)
	require.NoError(t, err)
	assert.Equal(t, wire, body.Config.Secret, "the forge was given a secret the verifier will not key its MAC with")
	assert.NotContains(t, body.Config.Secret, string(secret.Expose()), "the raw secret went out instead of its wire form")
}

func TestCloneAddressesCarryNoCredentialAndReDeriveTheKey(t *testing.T) {
	sess, key := sessionOn(t, answer(200, `{}`), "acme/pay")

	https, err := sess.CloneAddress(key, forge.HTTPS)
	require.NoError(t, err)
	assert.Equal(t, "x-access-token", https.HTTPUsername)
	got, err := repokey.Parse(https.URL)
	require.NoError(t, err, "repokey refuses a URL carrying a credential, so a parse is also that check")
	assert.Equal(t, key, got)

	ssh, err := sess.CloneAddress(key, forge.SSH)
	require.NoError(t, err)
	assert.Equal(t, "git", ssh.SSHAccount)
	got, err = repokey.Parse(ssh.URL)
	require.NoError(t, err)
	assert.Equal(t, key, got)
}

// TestARepositoryCreatedWithNoNameableCloneURLIsRefused: the key is derived
// from the clone URL, so a forge that created something it cannot name a clone
// URL for has not created anything this layer can address.
func TestARepositoryCreatedWithNoNameableCloneURLIsRefused(t *testing.T) {
	sess, key := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user") {
			io.WriteString(w, `{"login":"octo-tau","id":7}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"full_name":"acme/fresh","clone_url":"nowhere","default_branch":"main"}`)
	}), "acme/fresh")

	_, err := sess.Create(ctx(), key, forge.CreateOptions{})
	require.Error(t, err)
}

// TestInstallingOnARepositoryTheCredentialCannotAdministerFails. A hook and a
// deploy key both need more than push, and the failure is the forge's — late,
// and named as such.
func TestInstallingOnARepositoryTheCredentialCannotAdministerFails(t *testing.T) {
	sess, key := sessionOn(t, answer(http.StatusForbidden, `{"message":"Must have admin rights to Repository"}`), "acme/pay")

	_, err := sess.InstallDeployKey(ctx(), key, "tau", "ssh-ed25519 AAAA tau")
	require.Error(t, err)

	callback, err := url.Parse("https://build.example.invalid/hook")
	require.NoError(t, err)
	secret, err := forge.NewSecret()
	require.NoError(t, err)
	_, err = sess.RegisterPushHook(ctx(), key, callback, secret)
	require.Error(t, err)
}

// TestAHostDoesNotGetToChooseHowLongALogLineIs. A clone URL and a full name are
// both the forge's to write, and both land in an error message when they cannot
// be read; neither may be unbounded.
//
// THE SECOND CASE IS ON THIS DRIVER'S OWN HOST, and it is the one the length
// bound is really for. On another host the host check refuses the answer and the
// error is short whether or not anything measured the URL — measured: deleting
// the length check left the whole tree at exit 0 while only the off-host case
// existed. Here the URL is one repokey will happily parse, so without the bound
// a key IS derived from it, and a key is stored, indexed, compared and printed
// by everything downstream: the length an instance picks becomes a cost tau
// keeps paying.
func TestAHostDoesNotGetToChooseHowLongALogLineIs(t *testing.T) {
	long := strings.Repeat("a", 4000)

	t.Run("on another host", func(t *testing.T) {
		sess, key := sessionOn(t, answer(http.StatusOK,
			`{"full_name":"acme/`+long+`","clone_url":"https://x/acme/`+long+`.git","default_branch":"main"}`), "acme/pay")

		_, err := sess.Repository(ctx(), key)
		require.Error(t, err)
		assert.Less(t, len(err.Error()), 600, "the forge chose the length of this error")
	})

	t.Run("on this very host, where it would become a key", func(t *testing.T) {
		sess, key := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"full_name":"acme/`+long+`","clone_url":"https://`+r.Host+`/acme/`+long+`.git",
				"ssh_url":"ssh://git@`+r.Host+`/acme/`+long+`.git","default_branch":"main"}`)
		}), "acme/pay")

		repo, err := sess.Repository(ctx(), key)
		require.Error(t, err, "a %d-byte clone URL became the key %q", len(long), repo.Key)
		assert.Less(t, len(err.Error()), 600, "the forge chose the length of this error")
	})
}

// TestAListingRefusesWhatItCouldNotRemoveAndPagesToTheEnd: a listed hook or key
// with no id is one nothing could ever remove, a full page asks for the next,
// and a forge that never stops answering full pages is refused.
func TestAListingRefusesWhatItCouldNotRemoveAndPagesToTheEnd(t *testing.T) {
	sess, k := sessionOn(t, answer(http.StatusOK, `[{"id":0}]`), "acme/pay")
	_, err := sess.ListPushHooks(ctx(), k)
	require.Error(t, err)
	_, err = sess.ListDeployKeys(ctx(), k)
	require.Error(t, err)

	sess, k = sessionOn(t, answer(http.StatusNotFound, `{"message":"Not Found"}`), "acme/pay")
	_, err = sess.ListPushHooks(ctx(), k)
	require.Error(t, err)

	full := "[" + strings.TrimSuffix(strings.Repeat(`{"id":5,"config":{"url":"https://x.invalid/h"}},`, 100), ",") + "]"
	served := 0
	sess, k = sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			io.WriteString(w, `[]`)
			return
		}
		io.WriteString(w, full)
	}), "acme/pay")
	hooks, err := sess.ListPushHooks(ctx(), k)
	require.NoError(t, err)
	assert.Len(t, hooks, 100)
	assert.Equal(t, 2, served, "a full page asks for the next one")

	sess, k = sessionOn(t, answer(http.StatusOK, full), "acme/pay")
	_, err = sess.ListPushHooks(ctx(), k)
	require.ErrorContains(t, err, "refusing to keep paging")
}
