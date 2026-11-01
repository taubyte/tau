package frontend

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// The properties net/http's ServeMux gives and pkg/http does not.

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
			h.backend.get = func(id string) (*buildv2.Job, error) { asked = append(asked, id); return nil, errDenied }

			r := authed(http.MethodGet, "/job/"+testJobID, testToken)
			tc.mangle(r)
			h.do(t, r)

			require.Equal(t, []string{testJobID}, asked)
		})
	}
}

func TestAWildcardIsOneSegment(t *testing.T) {
	h := newHarness(t, Config{})

	for _, path := range []string{"/hook/a/b", "/hook/", "/job/a/b", "/jobs/a/b"} {
		t.Run(path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
			r.Header.Set("Content-Type", "application/json")
			require.Equal(t, http.StatusNotFound, h.do(t, r).Code)
		})
	}
}

func TestTheMethodIsPartOfTheRoute(t *testing.T) {
	h := newHarness(t, Config{})

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/hook/" + testHookID},
		{http.MethodPut, "/hook/" + testHookID},
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

func TestNothingElseIsServed(t *testing.T) {
	h := newHarness(t, Config{})

	for _, path := range []string{
		"/", "/ping", "/logs/bafycid", "/download/" + testJobID + "/resource-1",
		"/config/" + testProject, "/hook", "/github/" + testHookID, "/jobs", "/job",
	} {
		t.Run(path, func(t *testing.T) {
			require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, path, testToken)).Code)
		})
	}
}
