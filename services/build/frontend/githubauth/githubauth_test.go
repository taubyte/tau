package githubauth

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/build/frontend"
)

// This adapter is the one package under services/build that talks to a
// provider, and it is tested against a fake API rather than exempted from the
// bar: go-github's BaseURL is redirectable, so every branch below is reachable
// with no network and no token.
//
// The interface satisfaction lives here rather than in the source: importing
// frontend from the package that implements its Identity is fine — this is the
// frontend's own adapter and nothing else uses it — but asserting it in a test
// keeps the claim where a reader looks for it.
var _ frontend.Identity = (*Client)(nil)

func payKey(t *testing.T) repokey.Key {
	t.Helper()
	key, err := repokey.Parse("https://github.com/acme/pay")
	require.NoError(t, err)
	return key
}

// api serves one canned answer and records what was asked.
func api(t *testing.T, handle http.HandlerFunc) (*Client, *[]*http.Request) {
	t.Helper()

	var seen []*http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		handle(w, r)
	}))
	t.Cleanup(server.Close)

	c, err := New(WithBaseURL(server.URL))
	require.NoError(t, err)
	return c, &seen
}

func repository(perms map[string]bool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"name": "pay", "permissions": perms})
	}
}

func failure(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}
}

// ADMIN, MAINTAIN OR PUSH — services/auth/github.go's own rule. Read access is
// not enough, because a public repository reads to everyone.
func TestOnlyWriteAccessGrants(t *testing.T) {
	for _, tc := range []struct {
		name  string
		perms map[string]bool
		want  frontend.Access
	}{
		{"admin", map[string]bool{"admin": true}, frontend.AccessGranted},
		{"maintain", map[string]bool{"maintain": true}, frontend.AccessGranted},
		{"push", map[string]bool{"push": true}, frontend.AccessGranted},
		{"pull only", map[string]bool{"pull": true}, frontend.AccessDenied},
		{"triage only", map[string]bool{"triage": true}, frontend.AccessDenied},
		{"none at all", map[string]bool{}, frontend.AccessDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := api(t, repository(tc.perms))
			got, err := c.WriteAccess(t.Context(), "gho_token", payKey(t))
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// The question is asked with the CALLER's credential, which is what makes the
// answer about the caller and not about this cloud.
func TestTheCallersOwnTokenIsWhatAsks(t *testing.T) {
	c, seen := api(t, repository(map[string]bool{"push": true}))

	_, err := c.WriteAccess(t.Context(), "gho_caller", payKey(t))
	require.NoError(t, err)

	require.Len(t, *seen, 1)
	require.Equal(t, "/repos/acme/pay", (*seen)[0].URL.Path)
	require.Equal(t, "Bearer gho_caller", (*seen)[0].Header.Get("Authorization"))
}

func TestProviderAnswersBecomeAccessOrAFailureToDecide(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		want    frontend.Access
		decided bool
	}{
		{"401 is not a denial", failure(http.StatusUnauthorized, `{"message":"Bad credentials"}`), frontend.AccessUnauthenticated, true},
		{"403 is a denial", failure(http.StatusForbidden, `{"message":"Forbidden"}`), frontend.AccessDenied, true},
		{"404 is a denial", failure(http.StatusNotFound, `{"message":"Not Found"}`), frontend.AccessDenied, true},
		{"500 decides nothing", failure(http.StatusInternalServerError, `{"message":"boom"}`), frontend.AccessDenied, false},
		{"502 decides nothing", failure(http.StatusBadGateway, `{"message":"boom"}`), frontend.AccessDenied, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := api(t, tc.handler)
			got, err := c.WriteAccess(t.Context(), "gho_token", payKey(t))

			require.Equal(t, tc.want, got)
			if tc.decided {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

// A rate limit shares its status code with a denial and is not one. Letting it
// fall into the denial case beside it would turn a provider hiccup into "not
// yours" — a support call rather than a bug report.
func TestARateLimitIsNeverADenial(t *testing.T) {
	primary := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Limit", "60")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1700000000")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}
	secondary := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"You have exceeded a secondary rate limit","documentation_url":"https://docs.github.com/en/rest/overview/resources-in-the-rest-api#secondary-rate-limits"}`))
	}

	for name, handler := range map[string]http.HandlerFunc{"primary": primary, "secondary": secondary} {
		t.Run(name, func(t *testing.T) {
			c, _ := api(t, handler)
			got, err := c.WriteAccess(t.Context(), "gho_token", payKey(t))
			require.Equal(t, frontend.AccessDenied, got)
			require.ErrorContains(t, err, "rate limit")
		})
	}
}

func TestAProviderThisAdapterCannotReachDecidesNothing(t *testing.T) {
	c, err := New(WithBaseURL("http://127.0.0.1:1"))
	require.NoError(t, err)

	got, err := c.WriteAccess(t.Context(), "gho_token", payKey(t))

	require.Equal(t, frontend.AccessDenied, got)
	require.ErrorContains(t, err, "reaching github")
}

// A key names its own host, so an adapter that answered for every host would be
// answering about repositories it has never heard of — and "denied" would be a
// lie that looks like a decision.
func TestAKeyForAnotherProviderIsNotThisAdaptersToAnswer(t *testing.T) {
	for _, spelling := range []string{
		"https://gitlab.com/acme/pay",
		"https://gitlab.com/acme/team/widget",
		"https://git.internal:8443/acme/pay",
	} {
		t.Run(spelling, func(t *testing.T) {
			c, seen := api(t, repository(map[string]bool{"admin": true}))
			key, err := repokey.Parse(spelling)
			require.NoError(t, err)

			got, err := c.WriteAccess(t.Context(), "gho_token", key)

			require.Equal(t, frontend.AccessDenied, got)
			require.Error(t, err)
			require.Empty(t, *seen, "nothing was asked")
		})
	}
}

func TestARepositoryShapeGitHubDoesNotHaveIsRefusedBeforeItIsAsked(t *testing.T) {
	c, seen := api(t, repository(map[string]bool{"admin": true}))

	// repokey allows a bare repository at a host's root and a nested owner;
	// github.com has neither.
	for _, key := range []repokey.Key{
		{Host: Host, Name: "pay"},
		{Host: Host, Owner: "acme/team", Name: "widget"},
	} {
		got, err := c.WriteAccess(t.Context(), "gho_token", key)
		require.Equal(t, frontend.AccessDenied, got)
		require.ErrorContains(t, err, "github.com repository")
	}
	require.Empty(t, *seen)
}

func TestAnEmptyTokenIsUnauthenticatedWithoutAskingAnyone(t *testing.T) {
	c, seen := api(t, repository(map[string]bool{"admin": true}))

	got, err := c.WriteAccess(t.Context(), "", payKey(t))

	require.NoError(t, err)
	require.Equal(t, frontend.AccessUnauthenticated, got)
	require.Empty(t, *seen)
}

func TestOptions(t *testing.T) {
	t.Run("a base url without a trailing slash still works", func(t *testing.T) {
		c, seen := api(t, repository(map[string]bool{"push": true}))
		_, err := c.WriteAccess(t.Context(), "gho_token", payKey(t))
		require.NoError(t, err)
		require.Equal(t, "/repos/acme/pay", (*seen)[0].URL.Path)
	})

	t.Run("an unparseable base url is refused", func(t *testing.T) {
		c, err := New(WithBaseURL("://nope"))
		require.Nil(t, c)
		require.ErrorContains(t, err, "base url")
	})

	t.Run("a nil http client is refused", func(t *testing.T) {
		c, err := New(WithHTTPClient(nil))
		require.Nil(t, c)
		require.ErrorContains(t, err, "must not be nil")
	})

	// The transport is the caller's, so nothing here ever reaches the network:
	// the round tripper answers on its own.
	t.Run("a transport is used when given", func(t *testing.T) {
		var asked *http.Request
		client, err := New(WithHTTPClient(&http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
			asked = r
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"message":"Bad credentials"}`)),
				Request:    r,
			}, nil
		})}))
		require.NoError(t, err)

		got, err := client.WriteAccess(t.Context(), "gho_token", payKey(t))

		require.NoError(t, err)
		require.Equal(t, frontend.AccessUnauthenticated, got)
		require.NotNil(t, asked)
		require.Equal(t, "api.github.com", asked.URL.Host)
	})

	t.Run("no options at all is github.com itself", func(t *testing.T) {
		c, err := New()
		require.NoError(t, err)
		require.Nil(t, c.baseURL)
	})
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
