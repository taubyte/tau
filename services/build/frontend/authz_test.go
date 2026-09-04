package frontend

import (
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
)

// One allow and one deny per guarded route (service-conventions.md §6c), plus
// the cases v1's GitHubTokenHTTPAuth cannot express at all.

type route struct {
	name   string
	method string
	path   string
}

func guardedRoutes() []route {
	return []route{
		{"list", http.MethodGet, "/jobs/" + testProject},
		{"get", http.MethodGet, "/job/" + testJobID},
		{"cancel", http.MethodPost, "/cancel/" + testJobID},
		{"retry", http.MethodPost, "/retry/" + testJobID},
	}
}

func TestEveryJobRouteAllowsAWriterAndDeniesEverybodyElse(t *testing.T) {
	for _, r := range guardedRoutes() {
		t.Run(r.name, func(t *testing.T) {
			t.Run("allow", func(t *testing.T) {
				h := newHarness(t, Config{})
				w := h.do(t, authed(r.method, r.path, testToken))
				require.Less(t, w.Code, 300, "a writer must be served")
			})

			t.Run("deny", func(t *testing.T) {
				h := newHarness(t, Config{})
				h.identity.write = func(string, repokey.Key) (Access, error) { return AccessDenied, nil }
				w := h.do(t, authed(r.method, r.path, "somebody-elses-token"))
				require.Equal(t, http.StatusNotFound, w.Code)
			})

			t.Run("no credential", func(t *testing.T) {
				h := newHarness(t, Config{})
				reached := false
				h.backend.get = func(string) (*buildv1.Job, error) { reached = true; return nil, nil }
				h.projects.repositories = func(string) ([]string, error) { reached = true; return nil, nil }

				w := h.do(t, authed(r.method, r.path, ""))

				require.Equal(t, http.StatusUnauthorized, w.Code)
				// A caller who proved nothing does not get to find out whether
				// the thing they named exists.
				require.False(t, reached)
			})
		})
	}
}

// READ ACCESS IS NOT ENOUGH — services/auth/github.go's own rule, because a
// public repository reads to everyone. The Identity interface only reports
// write access, so this is the shape of the check rather than a flag on it.
func TestReadAccessIsNotWriteAccess(t *testing.T) {
	h := newHarness(t, Config{})
	asked := []repokey.Key{}
	h.identity.write = func(_ string, key repokey.Key) (Access, error) {
		asked = append(asked, key)
		return AccessDenied, nil // the shape a reader-not-writer produces
	}

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken))

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Equal(t, []repokey.Key{{Host: "github.com", Owner: "acme", Name: "pay"}}, asked)
}

func TestOnlyABearerSchemeIsRead(t *testing.T) {
	for _, header := range []string{"", "Basic dXNlcjpwYXNz", "Bearer", "Bearer ", "Token gho_x", "bearer gho_x"} {
		t.Run(header, func(t *testing.T) {
			h := newHarness(t, Config{})
			r := authed(http.MethodGet, "/job/"+testJobID, "")
			if header != "" {
				r.Header.Set("Authorization", header)
			}
			require.Equal(t, http.StatusUnauthorized, h.do(t, r).Code)
		})
	}
}

// A token that is not a token is a 401, not a 404: the caller can fix it.
func TestAnUnusableCredentialIsA401AndNotADenial(t *testing.T) {
	h := newHarness(t, Config{})
	h.identity.write = func(string, repokey.Key) (Access, error) { return AccessUnauthenticated, nil }

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID, "revoked"))

	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// A provider outage that silently became "not yours" is a support call rather
// than a bug report.
func TestAnUndecidedAccessIsNeverADenial(t *testing.T) {
	h := newHarness(t, Config{})
	h.identity.write = func(string, repokey.Key) (Access, error) {
		return AccessDenied, errors.New("github rate limit")
	}

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken))

	require.Equal(t, http.StatusBadGateway, w.Code)
	require.NotContains(t, bodyOf(t, w), "rate limit")
}

// A project is gated by more than one repository and they need not be on one
// provider, so an adapter that cannot answer for one must not shadow the grant
// the other would have given.
func TestOneUnanswerableRepositoryDoesNotShadowAGrant(t *testing.T) {
	h := newHarness(t, Config{})
	h.projects.repositories = func(string) ([]string, error) {
		return []string{"gitlab.com/acme/config", testRepoKey}, nil
	}
	h.identity.write = func(_ string, key repokey.Key) (Access, error) {
		if key.Host != "github.com" {
			return AccessDenied, errors.New("cannot answer for gitlab.com")
		}
		return AccessGranted, nil
	}

	w := h.do(t, authed(http.MethodGet, "/jobs/"+testProject, testToken))
	require.Equal(t, http.StatusOK, w.Code)
}

// And when nothing granted, the failure — not a denial — is what comes back.
func TestNothingGrantedAndSomethingUnansweredIsAFailure(t *testing.T) {
	h := newHarness(t, Config{})
	h.projects.repositories = func(string) ([]string, error) {
		return []string{"gitlab.com/acme/config", testRepoKey}, nil
	}
	h.identity.write = func(_ string, key repokey.Key) (Access, error) {
		if key.Host != "github.com" {
			return AccessDenied, errors.New("cannot answer for gitlab.com")
		}
		return AccessDenied, nil
	}

	w := h.do(t, authed(http.MethodGet, "/jobs/"+testProject, testToken))
	require.Equal(t, http.StatusBadGateway, w.Code)
}

// A key auth stored that this package cannot parse grants nothing, and must not
// stop the other one from granting.
func TestAnUnparseableRepositoryKeyGrantsNothingAndBlocksNothing(t *testing.T) {
	h := newHarness(t, Config{})
	h.projects.repositories = func(string) ([]string, error) {
		return []string{"GitHub.com/Acme/Pay", "", "github.com/acme/pay/", testRepoKey}, nil
	}

	w := h.do(t, authed(http.MethodGet, "/jobs/"+testProject, testToken))

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, h.logs.joined(), "unparseable repository key")
}

func TestAProjectWithNothingToGateItDeniesEveryone(t *testing.T) {
	h := newHarness(t, Config{})
	h.projects.repositories = func(string) ([]string, error) { return nil, nil }

	w := h.do(t, authed(http.MethodGet, "/jobs/"+testProject, testToken))
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestAnUnknownProjectIsTheSameAnswerAsADeniedOne(t *testing.T) {
	h := newHarness(t, Config{})
	h.projects.repositories = func(id string) ([]string, error) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no project"))
	}

	w := h.do(t, authed(http.MethodGet, "/jobs/nobody", testToken))
	require.Equal(t, http.StatusNotFound, w.Code)
}

// The backend's own Authorizer refusing THIS NODE is a deployment fault, and it
// is still answered as a 404 rather than confirming that the job exists.
func TestABackendPermissionDenialDoesNotConfirmExistence(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.get = func(string) (*buildv1.Job, error) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("peer may not read"))
	}

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken))
	require.Equal(t, http.StatusNotFound, w.Code)
}
