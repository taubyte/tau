package frontend

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	authv1 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1"
	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
)

// ingress_test.go holds the three properties net/http's own ServeMux gives and
// pkg/http does not, because each of them is a defect this role exists to
// remove rather than a behaviour to assume.

// pkg/http/context/helpers.go resolves a route variable from the QUERY first,
// then the mux vars, then a header, then the body — so on every v1 route with a
// path variable the caller can override it.
func TestAPathValueCannotBeOverriddenFromAnywhereElse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mangle func(*http.Request)
	}{
		{"query", func(r *http.Request) { r.URL.RawQuery = "jid=forged&projectId=forged&hook=forged" }},
		{"header", func(r *http.Request) {
			r.Header.Set("jid", "forged")
			r.Header.Set("projectId", "forged")
			r.Header.Set("hook", "forged")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, Config{})
			var asked []string
			h.backend.get = func(id string) (*buildv1.Job, error) { asked = append(asked, id); return nil, errDenied }

			r := authed(http.MethodGet, "/job/"+testJobID, testToken)
			tc.mangle(r)
			h.do(t, r)

			require.Equal(t, []string{testJobID}, asked)
		})
	}
}

// A {wildcard} spans exactly one segment, so a hook id can never contain a
// slash and can never reach a second path segment.
func TestAWildcardIsOneSegment(t *testing.T) {
	h := newHarness(t, Config{})

	for _, path := range []string{"/github/a/b", "/github/", "/job/a/b", "/jobs/a/b"} {
		t.Run(path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
			r.Header.Set(eventHeader, "push")
			r.Header.Set("Content-Type", "application/json")
			require.Equal(t, http.StatusNotFound, h.do(t, r).Code)
		})
	}
}

// A wrong method is a 405 rather than a route that quietly does not exist, so a
// misconfigured caller is told what is wrong.
func TestTheMethodIsPartOfTheRoute(t *testing.T) {
	h := newHarness(t, Config{})

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/github/" + testHookID},
		{http.MethodPut, "/github/" + testHookID},
		{http.MethodPost, "/job/" + testJobID},
		{http.MethodPost, "/jobs/" + testProject},
		{http.MethodGet, "/cancel/" + testJobID},
		{http.MethodGet, "/retry/" + testJobID},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := h.do(t, authed(tc.method, tc.path, testToken))
			require.Equal(t, http.StatusMethodNotAllowed, w.Code)
		})
	}
}

// Five routes and no others: no /ping, no bare-cid log route, no /download.
func TestNothingElseIsServed(t *testing.T) {
	h := newHarness(t, Config{})
	h.hooks.get = func(id string) (*authv1.GithubHook, error) {
		return &authv1.GithubHook{Id: id, Secret: testSecret, RepositoryKey: testRepoKey}, nil
	}

	for _, path := range []string{
		"/", "/ping", "/logs/bafycid", "/download/" + testJobID + "/resource-1",
		"/config/" + testProject, "/github", "/jobs", "/job",
	} {
		t.Run(path, func(t *testing.T) {
			require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, path, testToken)).Code)
		})
	}
}
