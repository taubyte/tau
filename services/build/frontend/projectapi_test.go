package frontend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/tenancy"
)

// linker is the register's own project table over a map, so a conflict or a
// role the route answers is the register's real answer and not a fake's idea
// of one. It is both the Linker and the Projects.
type linker struct {
	*register.Register
	links []register.Link
	fail  error
}

func newLinker(t *testing.T) *linker {
	t.Helper()
	r, err := register.New(register.WithStore(register.NewMemStore()))
	require.NoError(t, err)
	return &linker{Register: r}
}

// linkedTestProject is a register in which testUser linked testProject, made
// of testRepoKey.
func linkedTestProject(t *testing.T) *linker {
	t.Helper()
	l := newLinker(t)
	_, err := l.Register.LinkProject(t.Context(), register.Link{Project: testProject, Config: testRepoKey, Claimant: testUser})
	require.NoError(t, err)
	return l
}

func (l *linker) LinkProject(ctx context.Context, link register.Link) (register.ProjectRecord, error) {
	if l.fail != nil {
		return register.ProjectRecord{}, l.fail
	}
	rec, err := l.Register.LinkProject(ctx, link)
	if err == nil {
		l.links = append(l.links, link)
	}
	return rec, err
}

func (l *linker) ProjectsOf(ctx context.Context, user string) ([]register.ProjectRecord, error) {
	if l.fail != nil {
		return nil, l.fail
	}
	return l.Register.ProjectsOf(ctx, user)
}

func (l *linker) ProjectRecord(ctx context.Context, id string) (register.ProjectRecord, error) {
	if l.fail != nil {
		return register.ProjectRecord{}, l.fail
	}
	return l.Register.Project(ctx, id)
}

func (l *linker) GrantRole(ctx context.Context, project, grantee string, role register.Role, by string) (register.Grant, error) {
	if l.fail != nil {
		return register.Grant{}, l.fail
	}
	return l.Register.GrantRole(ctx, project, grantee, role, by)
}

func (l *linker) Revoke(ctx context.Context, project, id, by string) error {
	if l.fail != nil {
		return l.fail
	}
	return l.Register.Revoke(ctx, project, id, by)
}

func (l *linker) Grants(ctx context.Context, project string) ([]register.Grant, error) {
	if l.fail != nil {
		return nil, l.fail
	}
	return l.Register.Grants(ctx, project)
}

func (l *linker) Role(ctx context.Context, project, user string) (register.Role, error) {
	if l.fail != nil {
		return register.RoleNone, l.fail
	}
	return l.Register.RoleOf(ctx, project, user)
}

func (l *linker) Project(ctx context.Context, key string) (string, error) {
	rec, err := l.Register.Repository(ctx, key)
	return rec.Project, err
}

// users verifies "tok-<name>" as the tau user <name>.
type users struct{}

func (users) Caller(_ context.Context, token string) (string, error) {
	user, ok := strings.CutPrefix(token, "tok-")
	if !ok {
		return "", ErrBadCredential
	}
	return user, nil
}

// registered is a frontend whose project table and callers are the register's
// and users'.
func registered(t *testing.T, opts ...Option) (*harness, *linker) {
	t.Helper()
	l := newLinker(t)
	h := newHarness(t, Config{}, append([]Option{WithLinker(l), WithProjects(l), WithCallers(users{})}, opts...)...)
	return h, l
}

func linkRequestFor(token, id, body string) *http.Request {
	r := authed(http.MethodPost, "/projects/"+id, token)
	r.Body = http.NoBody
	if body != "" {
		r = withBody(r, []byte(body))
	}
	return r
}

const linkBody = `{"config_repository_key":"github.com/acme/cfg","code_repository_keys":["github.com/acme/pay"]}`

func linkOf(cfg string, code ...string) string {
	b, err := json.Marshal(linkRequest{Config: cfg, Code: code})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func projectsOf(t *testing.T, h *harness, token string) []string {
	t.Helper()
	w := h.do(t, authed(http.MethodGet, "/projects", token))
	require.Equal(t, http.StatusOK, w.Code)
	var mine []projectResponse
	require.NoError(t, json.Unmarshal([]byte(bodyOf(t, w)), &mine))
	var ids []string
	for _, p := range mine {
		ids = append(ids, p.ID)
	}
	return ids
}

func TestLinkingClaimsTheIdForTheCaller(t *testing.T) {
	h, l := registered(t)

	w := h.do(t, linkRequestFor("tok-alice", "QmPay", linkBody))
	body := bodyOf(t, w)
	require.Equal(t, http.StatusCreated, w.Code, body)
	var got projectResponse
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	require.Equal(t, "QmPay", got.ID)
	require.Equal(t, "github.com/acme/cfg", got.Config)
	require.Equal(t, []string{"github.com/acme/pay"}, got.Code)
	require.False(t, got.CreatedAt.IsZero())

	require.Equal(t, []register.Link{{
		Project: "QmPay", Config: "github.com/acme/cfg", Code: []string{"github.com/acme/pay"}, Claimant: "alice",
	}}, l.links, "the claimant is the tau user who linked, never this node")

	require.Equal(t, []string{"QmPay"}, projectsOf(t, h, "tok-alice"))
	require.Empty(t, projectsOf(t, h, "tok-bob"))
}

func TestAnyRepositoryLinks(t *testing.T) {
	h, _ := registered(t)
	for id, body := range map[string]string{
		"QmOrg":    linkOf("github.com/acme-org/payments", "github.com/acme-org/payments-api"),
		"QmNested": linkOf("gitlab.com/grp/sub/svc", "gitlab.com/grp/sub/deeper/lib"),
		"QmPlain":  linkOf("bitbucket.org/workspace/tb_not-special"),
	} {
		w := h.do(t, linkRequestFor("tok-alice", id, body))
		require.Equal(t, http.StatusCreated, w.Code, id+": "+bodyOf(t, w))
	}
	require.ElementsMatch(t, []string{"QmOrg", "QmNested", "QmPlain"}, projectsOf(t, h, "tok-alice"))
}

func TestLinkingAConfigOnlyProject(t *testing.T) {
	h, _ := registered(t)
	w := h.do(t, linkRequestFor("tok-alice", "QmPay", `{"config_repository_key":"github.com/acme/cfg"}`))
	require.Equal(t, http.StatusCreated, w.Code)
	require.Contains(t, bodyOf(t, w), `"code_repository_keys":[]`)
}

func TestTenancyIsCheckedOnEveryLinkedKey(t *testing.T) {
	served, err := tenancy.Parse([]string{"github.com/acme"})
	require.NoError(t, err)
	h, l := registered(t, WithTenancy(served))

	w := h.do(t, linkRequestFor("tok-alice", "QmPay", linkOf("github.com/acme/cfg", "github.com/elsewhere/pay")))
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Empty(t, l.links)
	require.Equal(t, http.StatusCreated, h.do(t, linkRequestFor("tok-alice", "QmPay", linkBody)).Code)
}

func TestAClaimedIdIsAConflictThatSaysWhatToDo(t *testing.T) {
	h, _ := registered(t)
	require.Equal(t, http.StatusCreated, h.do(t, linkRequestFor("tok-alice", "QmPay", linkBody)).Code)

	for _, body := range []string{linkBody, linkOf("github.com/acme/other")} {
		w := h.do(t, linkRequestFor("tok-bob", "QmPay", body))
		require.Equal(t, http.StatusConflict, w.Code)
		require.Contains(t, bodyOf(t, w), "project QmPay is already claimed; generate a new id in project.yaml")
	}

	w := h.do(t, linkRequestFor("tok-bob", "QmOther", linkOf("github.com/acme/other", "github.com/acme/pay")))
	require.Equal(t, http.StatusConflict, w.Code, "a code repository belongs to one project")
	require.Contains(t, bodyOf(t, w), "github.com/acme/pay is project QmPay's")
}

func TestTheOwnerRelinkReplacesTheRepositories(t *testing.T) {
	h, l := registered(t)
	require.Equal(t, http.StatusCreated, h.do(t, linkRequestFor("tok-alice", "QmPay", linkBody)).Code)

	w := h.do(t, linkRequestFor("tok-alice", "QmPay", linkOf("github.com/acme-renamed/cfg", "github.com/acme/web")))
	require.Equal(t, http.StatusCreated, w.Code, bodyOf(t, w))
	rec, err := l.Register.Project(t.Context(), "QmPay")
	require.NoError(t, err)
	require.Equal(t, "github.com/acme-renamed/cfg", rec.Config)
	require.Equal(t, []string{"github.com/acme/web"}, rec.Code)
	require.Equal(t, "alice", rec.Claimant)
	require.True(t, l.links[1].Replace)

	require.Equal(t, http.StatusCreated, h.do(t, linkRequestFor("tok-bob", "QmBob", linkOf("github.com/acme/cfg", "github.com/acme/pay"))).Code,
		"what the relink released is free")

	// a granted owner relinks too, and the project stays the claimant's
	_, err = l.Register.GrantRole(t.Context(), "QmPay", "carol", register.RoleOwner, "alice")
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, h.do(t, linkRequestFor("tok-carol", "QmPay", linkOf("github.com/acme-renamed/cfg"))).Code)
	rec, err = l.Register.Project(t.Context(), "QmPay")
	require.NoError(t, err)
	require.Equal(t, "alice", rec.Claimant)
	require.Empty(t, rec.Code)
}

func TestLinkingRefusals(t *testing.T) {
	h, l := registered(t)

	type testCase struct {
		token, id, body string
		want            int
	}
	for name, tc := range map[string]testCase{
		"no token":           {"", "QmPay", linkBody, http.StatusUnauthorized},
		"a bad token":        {"nope", "QmPay", linkBody, http.StatusUnauthorized},
		"not json":           {"tok-alice", "QmPay", "{", http.StatusBadRequest},
		"no config":          {"tok-alice", "QmPay", `{"code_repository_keys":["github.com/acme/pay"]}`, http.StatusBadRequest},
		"a key that is not":  {"tok-alice", "QmPay", `{"config_repository_key":"nope"}`, http.StatusBadRequest},
		"too many":           {"tok-alice", "QmPay", `{"config_repository_key":"github.com/acme/cfg","code_repository_keys":[` + strings.Repeat(`"github.com/a/b",`, maxLinkedRepositories) + `"github.com/a/c"]}`, http.StatusBadRequest},
		"an id with a slash": {"tok-alice", "a%2Fb", linkBody, http.StatusBadRequest},
	} {
		r := linkRequestFor(tc.token, tc.id, tc.body)
		if tc.token == "" {
			r.Header.Del("Authorization")
		}
		w := h.do(t, r)
		require.Equal(t, tc.want, w.Code, name)
	}
	require.Empty(t, l.links, "nothing was claimed")
}

func TestARegisterThatFailsIsNotAConflict(t *testing.T) {
	h, l := registered(t)
	l.fail = errors.New("register unreachable")

	require.Equal(t, http.StatusBadGateway, h.do(t, linkRequestFor("tok-alice", "QmPay", linkBody)).Code)
	require.Equal(t, http.StatusBadGateway, h.do(t, authed(http.MethodGet, "/projects", "tok-alice")).Code)
	require.Equal(t, http.StatusUnauthorized, h.do(t, authed(http.MethodGet, "/projects", "nope")).Code)
}

func TestARegisterRefusingTheLinkItselfIsSaid(t *testing.T) {
	h := newHarness(t, Config{}, WithLinker(&refusing{err: register.ErrNoProject}))
	h.projects.role = func(string, string) (register.Role, error) { return register.RoleNone, register.ErrNotLinked }
	require.Equal(t, http.StatusBadRequest, h.do(t, linkRequestFor(testToken, "QmPay", linkBody)).Code)

	h.projects.role = func(string, string) (register.Role, error) {
		return register.RoleNone, connect.NewError(connect.CodeInvalidArgument, errors.New("bad id"))
	}
	require.Equal(t, http.StatusBadRequest, h.do(t, linkRequestFor(testToken, "QmPay", linkBody)).Code)

	h.projects.role = func(string, string) (register.Role, error) { return register.RoleNone, errors.New("down") }
	require.Equal(t, http.StatusBadGateway, h.do(t, linkRequestFor(testToken, "QmPay", linkBody)).Code)
}

type refusing struct {
	Linker
	err error
}

func (r *refusing) LinkProject(context.Context, register.Link) (register.ProjectRecord, error) {
	return register.ProjectRecord{}, r.err
}

func TestTheProjectRoutesWithoutALinkerAreADenial(t *testing.T) {
	h := newHarness(t, Config{})
	require.Equal(t, http.StatusNotFound, h.do(t, linkRequestFor(testToken, "QmPay", linkBody)).Code)
	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, "/projects", testToken)).Code)
	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, "/projects/"+testProject, testToken)).Code)
	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, "/projects/"+testProject+"/grants", testToken)).Code)
	require.Error(t, WithLinker(nil)(&options{}))
}

func TestAnUnlinkedProjectIsADenial(t *testing.T) {
	h := newHarness(t, Config{})
	h.projects.role = func(_, id string) (register.Role, error) {
		return register.RoleNone, fmt.Errorf("%w: project %s", register.ErrNotLinked, id)
	}
	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, "/jobs/QmNobody", testToken)).Code)
}

func TestReadingAProject(t *testing.T) {
	h, l := registered(t)
	require.Equal(t, http.StatusCreated, h.do(t, linkRequestFor("tok-alice", "QmPay", linkBody)).Code)

	w := h.do(t, authed(http.MethodGet, "/projects/QmPay", "tok-alice"))
	require.Equal(t, http.StatusOK, w.Code)
	var got projectResponse
	require.NoError(t, json.Unmarshal([]byte(bodyOf(t, w)), &got))
	require.Equal(t, "github.com/acme/cfg", got.Config)
	require.Equal(t, []string{"github.com/acme/pay"}, got.Code)

	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, "/projects/QmNone", "tok-alice")).Code)
	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, "/projects/QmPay", "tok-bob")).Code)

	l.fail = errors.New("register down")
	require.Equal(t, http.StatusBadGateway, h.do(t, authed(http.MethodGet, "/projects/QmPay", "tok-alice")).Code)
}

func TestReadingAProjectTheRegisterLostIsADenial(t *testing.T) {
	h := newHarness(t, Config{}, WithLinker(newLinker(t)))
	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, "/projects/"+testProject, testToken)).Code)
}
