package ingress

import (
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/pkg/register"
)

type route struct {
	name   string
	method string
	path   string
	want   register.Role
}

func guardedRoutes() []route {
	return []route{
		{"list", http.MethodGet, "/jobs/" + testProject, register.RoleViewer},
		{"get", http.MethodGet, "/job/" + testJobID, register.RoleViewer},
		{"cancel", http.MethodPost, "/cancel/" + testJobID, register.RoleBuilder},
		{"retry", http.MethodPost, "/retry/" + testJobID, register.RoleBuilder},
		{"project", http.MethodGet, "/projects/" + testProject, register.RoleViewer},
		{"secrets", http.MethodGet, "/secrets/" + testProject + "/main", register.RoleSecretManager},
		{"grants", http.MethodGet, "/projects/" + testProject + "/grants", register.RoleOwner},
	}
}

var everyRole = []register.Role{register.RoleNone, register.RoleViewer, register.RoleBuilder, register.RoleSecretManager, register.RoleOwner}

func TestEveryRouteServesItsRoleAndAboveAndDeniesBelow(t *testing.T) {
	for _, r := range guardedRoutes() {
		for _, role := range everyRole {
			t.Run(r.name+"/"+string(role), func(t *testing.T) {
				h := newHarness(t, Config{}, WithLinker(linkedTestProject(t)), WithVault(&fakeVault{}))
				h.projects.role = func(string, string) (register.Role, error) { return role, nil }
				w := h.do(t, authed(r.method, r.path, testToken))
				if role.Includes(r.want) {
					require.Less(t, w.Code, 300, bodyOf(t, w))
				} else {
					require.Equal(t, http.StatusNotFound, w.Code)
				}
			})
		}
	}
}

func TestRolesIncludeTheOnesBelowThem(t *testing.T) {
	require.True(t, register.RoleSecretManager.Includes(register.RoleBuilder))
	require.True(t, register.RoleOwner.Includes(register.RoleSecretManager))
	require.False(t, register.RoleBuilder.Includes(register.RoleSecretManager))
	require.False(t, register.RoleNone.Includes(register.RoleNone))
}

func TestACallerWhoProvedNothingLearnsNothing(t *testing.T) {
	for _, r := range guardedRoutes() {
		t.Run(r.name, func(t *testing.T) {
			h := newHarness(t, Config{}, WithLinker(linkedTestProject(t)), WithVault(&fakeVault{}))
			reached := false
			h.jobs.get = func(string) (*plmv2.Job, error) { reached = true; return nil, nil }
			h.projects.role = func(string, string) (register.Role, error) { reached = true; return register.RoleOwner, nil }

			require.Equal(t, http.StatusUnauthorized, h.do(t, authed(r.method, r.path, "")).Code)
			require.Equal(t, http.StatusUnauthorized, h.do(t, authed(r.method, r.path, "not-a-tau-token")).Code)
			require.False(t, reached)
		})
	}
}

func TestOnlyABearerSchemeIsRead(t *testing.T) {
	for _, header := range []string{"", "Basic dXNlcjpwYXNz", "Bearer", "Bearer ", "Token x", "bearer x"} {
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

func TestAuthsAnswersAboutACaller(t *testing.T) {
	type testCase struct {
		name string
		err  error
		user string
		want int
	}
	for _, c := range []testCase{
		{"unauthenticated", connect.NewError(connect.CodeUnauthenticated, errors.New("revoked")), "", http.StatusUnauthorized},
		{"no user", nil, "", http.StatusUnauthorized},
		{"auth is down", errors.New("dial"), "", http.StatusBadGateway},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, Config{})
			h.callers.caller = func(string) (string, error) { return c.user, c.err }
			w := h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken))
			require.Equal(t, c.want, w.Code)
			require.NotContains(t, bodyOf(t, w), "dial")
		})
	}
}

func TestTheRoleIsAskedForTheCallerOnTheJobsProject(t *testing.T) {
	h := newHarness(t, Config{})
	var asked []string
	h.projects.role = func(project, user string) (register.Role, error) {
		asked = append(asked, project+" "+user)
		return register.RoleViewer, nil
	}
	require.Equal(t, http.StatusOK, h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken)).Code)
	require.Equal(t, []string{testProject + " " + testUser}, asked)
}

func TestAJobWhoseRepositoryNoProjectOwnsIsDenied(t *testing.T) {
	h := newHarness(t, Config{})
	h.jobs.get = func(id string) (*plmv2.Job, error) {
		return &plmv2.Job{Id: id, RepositoryKey: "github.com/acme/orphan"}, nil
	}
	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken)).Code)
}

func TestARoleThatCannotBeReadIsNeverADenial(t *testing.T) {
	h := newHarness(t, Config{})
	h.projects.role = func(string, string) (register.Role, error) {
		return register.RoleNone, connect.NewError(connect.CodeUnavailable, errors.New("register down"))
	}
	require.Equal(t, http.StatusServiceUnavailable, h.do(t, authed(http.MethodGet, "/jobs/"+testProject, testToken)).Code)
	require.Equal(t, http.StatusServiceUnavailable, h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken)).Code)
}

func TestAnUnknownProjectIsTheSameAnswerAsADeniedOne(t *testing.T) {
	h := newHarness(t, Config{})
	w := h.do(t, authed(http.MethodGet, "/jobs/nobody", testToken))
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestABackendPermissionDenialDoesNotConfirmExistence(t *testing.T) {
	h := newHarness(t, Config{})
	h.jobs.get = func(string) (*plmv2.Job, error) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("peer may not read"))
	}

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken))
	require.Equal(t, http.StatusNotFound, w.Code)
}
