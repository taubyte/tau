package frontend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/register"
)

// linker is the register's own project table over a map, so a conflict the
// route answers is the register's real refusal and not a fake's idea of one.
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

func (l *linker) ProjectsOf(ctx context.Context, claimant string) ([]register.ProjectRecord, error) {
	if l.fail != nil {
		return nil, l.fail
	}
	return l.Register.ProjectsOf(ctx, claimant)
}

func (l *linker) ProjectRecord(ctx context.Context, id string) (register.ProjectRecord, error) {
	if l.fail != nil {
		return register.ProjectRecord{}, l.fail
	}
	return l.Register.Project(ctx, id)
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

func TestLinkingClaimsTheIdForTheCaller(t *testing.T) {
	l := newLinker(t)
	h := newHarness(t, Config{}, WithLinker(l))

	w := h.do(t, linkRequestFor(testToken, "QmPay", linkBody))
	body := bodyOf(t, w)
	require.Equal(t, http.StatusCreated, w.Code, body)
	var got projectResponse
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	require.Equal(t, "QmPay", got.ID)
	require.Equal(t, "github.com/acme/cfg", got.Config)
	require.Equal(t, []string{"github.com/acme/pay"}, got.Code)
	require.False(t, got.CreatedAt.IsZero())

	require.Equal(t, []register.Link{{
		Project: "QmPay", Config: "github.com/acme/cfg", Code: []string{"github.com/acme/pay"},
		Claimant: "github.com/1001",
	}}, l.links, "the claimant is the caller the forge named, never this node")

	// And it is the caller's.
	w = h.do(t, authed(http.MethodGet, "/forges/github.com/projects", testToken))
	require.Equal(t, http.StatusOK, w.Code)
	var mine []projectResponse
	require.NoError(t, json.Unmarshal([]byte(bodyOf(t, w)), &mine))
	require.Len(t, mine, 1)
	require.Equal(t, "QmPay", mine[0].ID)
}

func TestLinkingAConfigOnlyProject(t *testing.T) {
	h := newHarness(t, Config{}, WithLinker(newLinker(t)))
	w := h.do(t, linkRequestFor(testToken, "QmPay", `{"config_repository_key":"github.com/acme/cfg"}`))
	require.Equal(t, http.StatusCreated, w.Code)
	require.Contains(t, bodyOf(t, w), `"code_repository_keys":[]`)
}

// Write to one of them is not enough: my config over your code would build
// your code under my project.
func TestLinkingNeedsWriteToEveryRepository(t *testing.T) {
	l := newLinker(t)
	h := newHarness(t, Config{}, WithLinker(l))
	h.identity.write = func(_ string, key repokey.Key) (Access, error) {
		if strings.HasSuffix(key.String(), "/pay") {
			return AccessDenied, nil
		}
		return AccessGranted, nil
	}

	w := h.do(t, linkRequestFor(testToken, "QmPay", linkBody))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, l.links, "nothing reached the register")
}

// AN ID ANOTHER REPOSITORY CLAIMED IS REFUSED BY NAME, with the fix: the id is
// the author's, so generating a new one is theirs to do.
func TestAClaimedIdIsAConflictThatSaysWhatToDo(t *testing.T) {
	l := newLinker(t)
	h := newHarness(t, Config{}, WithLinker(l))
	require.Equal(t, http.StatusCreated, h.do(t, linkRequestFor(testToken, "QmPay", linkBody)).Code)

	w := h.do(t, linkRequestFor(testToken, "QmPay", `{"config_repository_key":"github.com/acme/other"}`))
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, bodyOf(t, w), "project QmPay is already claimed by github.com/acme/cfg; generate a new id in project.yaml")

	w = h.do(t, linkRequestFor(testToken, "QmOther", `{"config_repository_key":"github.com/acme/other","code_repository_keys":["github.com/acme/pay"]}`))
	require.Equal(t, http.StatusConflict, w.Code, "a code repository belongs to one project")
}

func TestLinkingRefusals(t *testing.T) {
	l := newLinker(t)
	h := newHarness(t, Config{}, WithLinker(l))

	for name, tc := range map[string]struct {
		token, id, body string
		want            int
	}{
		"no token":           {"", "QmPay", linkBody, http.StatusUnauthorized},
		"not json":           {testToken, "QmPay", "{", http.StatusBadRequest},
		"no config":          {testToken, "QmPay", `{"code_repository_keys":["github.com/acme/pay"]}`, http.StatusBadRequest},
		"a key that is not":  {testToken, "QmPay", `{"config_repository_key":"nope"}`, http.StatusBadRequest},
		"too many":           {testToken, "QmPay", `{"config_repository_key":"github.com/acme/cfg","code_repository_keys":[` + strings.Repeat(`"github.com/a/b",`, maxLinkedRepositories) + `"github.com/a/c"]}`, http.StatusBadRequest},
		"an id with a slash": {testToken, "a%2Fb", linkBody, http.StatusBadRequest},
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

func TestLinkingWhenTheForgeCannotSayWhoTheCallerIs(t *testing.T) {
	l := newLinker(t)
	h := newHarness(t, Config{}, WithLinker(l))

	for err, want := range map[error]int{
		ErrBadCredential:            http.StatusUnauthorized,
		ErrUnknownForge:             http.StatusNotFound,
		errors.New("forge is down"): http.StatusBadGateway,
	} {
		h.identity.claimant = func(string, string) (string, error) { return "", err }
		require.Equal(t, want, h.do(t, linkRequestFor(testToken, "QmPay", linkBody)).Code, err.Error())
		require.Equal(t, want, h.do(t, authed(http.MethodGet, "/forges/github.com/projects", testToken)).Code, err.Error())
	}
	require.Empty(t, l.links)
}

func TestARegisterThatFailsIsNotAConflict(t *testing.T) {
	l := newLinker(t)
	l.fail = errors.New("register unreachable")
	h := newHarness(t, Config{}, WithLinker(l))

	require.Equal(t, http.StatusBadGateway, h.do(t, linkRequestFor(testToken, "QmPay", linkBody)).Code)
	require.Equal(t, http.StatusBadGateway, h.do(t, authed(http.MethodGet, "/forges/github.com/projects", testToken)).Code)
}

func TestTheProjectRoutesWithoutALinkerAreADenial(t *testing.T) {
	h := newHarness(t, Config{})
	require.Equal(t, http.StatusNotFound, h.do(t, linkRequestFor(testToken, "QmPay", linkBody)).Code)
	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, "/forges/github.com/projects", testToken)).Code)
	require.Error(t, WithLinker(nil)(&options{}))
}

// A project the register never linked is the same 404 a denial is, on every
// route that authorizes against a project.
func TestAnUnlinkedProjectIsADenial(t *testing.T) {
	h := newHarness(t, Config{})
	h.projects.repositories = func(id string) ([]string, error) {
		return nil, fmt.Errorf("%w: project %s", register.ErrNotLinked, id)
	}
	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, "/jobs/QmNobody", testToken)).Code)
}

// A project is read by anyone who may write to one of its repositories, not
// only by whoever linked it, and an id they may not see is a 404.
func TestReadingAProject(t *testing.T) {
	l := newLinker(t)
	h := newHarness(t, Config{}, WithLinker(l))
	h.projects.repositories = func(id string) ([]string, error) {
		rec, err := l.Register.Project(context.Background(), id)
		return rec.Repositories(), err
	}
	require.Equal(t, http.StatusCreated, h.do(t, linkRequestFor(testToken, "QmPay", linkBody)).Code)

	w := h.do(t, authed(http.MethodGet, "/projects/QmPay", testToken))
	require.Equal(t, http.StatusOK, w.Code)
	var got projectResponse
	require.NoError(t, json.Unmarshal([]byte(bodyOf(t, w)), &got))
	require.Equal(t, "github.com/acme/cfg", got.Config)
	require.Equal(t, []string{"github.com/acme/pay"}, got.Code)

	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, "/projects/QmNone", testToken)).Code)
	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, "/projects/QmPay", "someone-else")).Code)

	l.fail = errors.New("register down")
	require.Equal(t, http.StatusBadGateway, h.do(t, authed(http.MethodGet, "/projects/QmPay", testToken)).Code)

	bare := newHarness(t, Config{})
	require.Equal(t, http.StatusNotFound, bare.do(t, authed(http.MethodGet, "/projects/"+testProject, testToken)).Code)
}
