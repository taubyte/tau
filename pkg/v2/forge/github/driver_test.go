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

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/v2/forge"
	"github.com/taubyte/tau/pkg/v2/forge/github"
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

// TestARedirectOffTheHostDoesNotCarryTheCredential is the threat PinnedTransport
// exists for: an http.Client follows redirects, and a forge — or anything
// answering as one — that answers 302 to somewhere else would otherwise be
// handed the Authorization header on the next hop.
func TestARedirectOffTheHostDoesNotCarryTheCredential(t *testing.T) {
	var secondHop int
	sess, key := sessionOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "" && strings.Contains(r.URL.Path, "/api/v3/repos/") {
			http.Redirect(w, r, "https://elsewhere.example.invalid/api/v3/user", http.StatusFound)
			return
		}
		secondHop++
		io.WriteString(w, `{}`)
	}), "acme/pay")

	_, err := sess.Access(ctx(), key)
	require.Error(t, err)
	var leak *forge.CredentialLeakError
	require.ErrorAs(t, err, &leak, "the redirect was followed with the credential attached")
	assert.Equal(t, 0, secondHop)
}

// ─────────────────────────────── access ───────────────────────────────

func TestAccessMapsGitHubsRolesOntoTausRungs(t *testing.T) {
	for name, tc := range map[string]struct {
		perms string
		want  forge.Access
	}{
		"admin":                        {`{"admin":true,"maintain":true,"push":true,"pull":true}`, forge.AccessAdmin},
		"maintain":                     {`{"maintain":true,"push":true,"pull":true}`, forge.AccessWrite},
		"push":                         {`{"push":true,"pull":true}`, forge.AccessWrite},
		"pull only":                    {`{"pull":true}`, forge.AccessRead},
		"triage, which grants no push": {`{"pull":true,"triage":true}`, forge.AccessRead},
		"nothing at all":               {`{}`, forge.AccessNone},
	} {
		t.Run(name, func(t *testing.T) {
			body := `{"full_name":"acme/pay","default_branch":"main","permissions":` + tc.perms + `,"clone_url":"https://x/acme/pay.git"}`
			sess, key := sessionOn(t, answer(http.StatusOK, body), "acme/pay")
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
func TestAHostDoesNotGetToChooseHowLongALogLineIs(t *testing.T) {
	long := strings.Repeat("a", 4000)
	sess, key := sessionOn(t, answer(http.StatusOK,
		`{"full_name":"acme/`+long+`","clone_url":"https://x/acme/`+long+`.git","default_branch":"main"}`), "acme/pay")

	_, err := sess.Repository(ctx(), key)
	require.Error(t, err)
	assert.Less(t, len(err.Error()), 600, "the forge chose the length of this error")
}
