package github_test

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

func TestAccessMapsGitHubsRolesOntoTausRungs(t *testing.T) {
	for name, tc := range map[string]struct {
		perms string
		want  forge.Access
	}{
		"admin":    {`{"admin":true,"maintain":true,"push":true,"pull":true}`, forge.AccessAdmin},
		"maintain": {`{"maintain":true,"push":true,"pull":true}`, forge.AccessWrite},
		// GitHub sends push:true with maintain; this row keeps maintain alone in the
		// write predicate.
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

func TestARepositoryWhoseCloneURLNamesNothingIsRefused(t *testing.T) {
	sess, key := sessionOn(t, answer(http.StatusOK, `{"full_name":"acme/pay","clone_url":"","default_branch":"main"}`), "acme/pay")
	_, err := sess.Repository(ctx(), key)
	require.Error(t, err)
}

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
