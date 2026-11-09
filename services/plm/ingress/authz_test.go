package ingress

import (
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/services/auth/projects"
)

type route struct {
	name   string
	method string
	path   string
	want   projects.Role
}

func guardedRoutes() []route {
	return []route{
		{"list", http.MethodGet, "/jobs/" + testProject, projects.RoleViewer},
		{"get", http.MethodGet, "/job/" + testJobID, projects.RoleViewer},
		{"cancel", http.MethodPost, "/cancel/" + testJobID, projects.RoleBuilder},
		{"retry", http.MethodPost, "/retry/" + testJobID, projects.RoleBuilder},
		{"project", http.MethodGet, "/projects/" + testProject, projects.RoleViewer},
		{"secrets", http.MethodGet, "/secrets/" + testProject + "/main", projects.RoleSecretManager},
		{"grants", http.MethodGet, "/projects/" + testProject + "/grants", projects.RoleOwner},
	}
}

var everyRole = []projects.Role{projects.RoleNone, projects.RoleViewer, projects.RoleBuilder, projects.RoleSecretManager, projects.RoleOwner}

func TestEveryRouteServesItsRoleAndAboveAndDeniesBelow(t *testing.T) {
	for _, r := range guardedRoutes() {
		for _, role := range everyRole {
			t.Run(r.name+"/"+string(role), func(t *testing.T) {
				h := newHarness(t, Config{}, WithLinker(linkedTestProject(t)), WithVault(&fakeVault{}))
				h.projects.role = func(string, string) (projects.Role, error) { return role, nil }
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
	require.True(t, projects.RoleSecretManager.Includes(projects.RoleBuilder))
	require.True(t, projects.RoleOwner.Includes(projects.RoleSecretManager))
	require.False(t, projects.RoleBuilder.Includes(projects.RoleSecretManager))
	require.False(t, projects.RoleNone.Includes(projects.RoleNone))
}

func TestACallerWhoProvedNothingLearnsNothing(t *testing.T) {
	for _, r := range guardedRoutes() {
		t.Run(r.name, func(t *testing.T) {
			h := newHarness(t, Config{}, WithLinker(linkedTestProject(t)), WithVault(&fakeVault{}))
			reached := false
			h.jobs.get = func(string) (*plmv2.Job, error) { reached = true; return nil, nil }
			h.projects.role = func(string, string) (projects.Role, error) { reached = true; return projects.RoleOwner, nil }

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
	h.projects.role = func(project, user string) (projects.Role, error) {
		asked = append(asked, project+" "+user)
		return projects.RoleViewer, nil
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
	h.projects.role = func(string, string) (projects.Role, error) {
		return projects.RoleNone, connect.NewError(connect.CodeUnavailable, errors.New("register down"))
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
