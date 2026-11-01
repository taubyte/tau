package frontend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/register"
)

func manualBuild(token, project string, body any) *http.Request {
	raw, _ := json.Marshal(body)
	return withBody(authed(http.MethodPost, "/build/"+project, token), raw)
}

func asBuilder(project, user string) (register.Role, error) {
	if project != testProject {
		return register.RoleNone, connect.NewError(connect.CodeNotFound, fmt.Errorf("no project %s", project))
	}
	if user == testUser {
		return register.RoleBuilder, nil
	}
	return register.RoleViewer, nil
}

func TestABuilderQueuesABuildByHand(t *testing.T) {
	h := newHarness(t, Config{})
	h.projects.role = asBuilder

	w := h.do(t, manualBuild(testToken, testProject, buildRequest{Repository: testRepoKey, Ref: "refs/heads/main", Commit: testSHA}))
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())

	var got queued
	require.NoError(t, json.NewDecoder(strings.NewReader(bodyOf(t, w))).Decode(&got))
	require.Equal(t, testJobID, got.JobID)
	require.Equal(t, []enqueued{{testRepoKey, "refs/heads/main", testSHA}}, h.backend.enqueues())
}

func TestWhatAManualBuildRefuses(t *testing.T) {
	for name, c := range map[string]struct {
		token   string
		project string
		body    any
		role    func(project, user string) (register.Role, error)
		owner   func(key string) (string, error)
		status  int
		says    string
	}{
		"a viewer": {
			role: func(string, string) (register.Role, error) { return register.RoleViewer, nil },
			body: buildRequest{Repository: testRepoKey, Ref: "refs/heads/main", Commit: testSHA}, status: http.StatusNotFound,
		},
		"no credential": {
			token: "-", body: buildRequest{Repository: testRepoKey, Ref: "refs/heads/main", Commit: testSHA}, status: http.StatusUnauthorized,
		},
		"a body that is not one": {
			body: "nonsense", status: http.StatusBadRequest, says: "repository_key",
		},
		"a key that is not canonical": {
			body: buildRequest{Repository: "GitHub.com/Acme/Pay", Ref: "refs/heads/main", Commit: testSHA}, status: http.StatusBadRequest, says: "not a repository key",
		},
		"another project's repository": {
			owner: func(string) (string, error) { return "proj-2", nil },
			body:  buildRequest{Repository: testRepoKey, Ref: "refs/heads/main", Commit: testSHA}, status: http.StatusUnprocessableEntity, says: "not linked to this project",
		},
		"a repository nobody linked": {
			owner: func(string) (string, error) { return "", register.ErrNotLinked },
			body:  buildRequest{Repository: testRepoKey, Ref: "refs/heads/main", Commit: testSHA}, status: http.StatusUnprocessableEntity, says: "not linked to this project",
		},
		"a register that cannot answer": {
			owner: func(string) (string, error) { return "", connect.NewError(connect.CodeUnavailable, errors.New("down")) },
			body:  buildRequest{Repository: testRepoKey, Ref: "refs/heads/main", Commit: testSHA}, status: http.StatusServiceUnavailable,
		},
		"a short commit": {
			body: buildRequest{Repository: testRepoKey, Ref: "refs/heads/main", Commit: "1111"}, status: http.StatusBadRequest, says: "40-character",
		},
		"a ref this cloud does not build": {
			body: buildRequest{Repository: testRepoKey, Ref: "refs/heads/feature", Commit: testSHA}, status: http.StatusUnprocessableEntity, says: "refs/heads/feature is not a ref this cloud builds",
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, Config{})
			h.projects.role = asBuilder
			if c.role != nil {
				h.projects.role = c.role
			}
			h.projects.project = c.owner
			token := testToken
			if c.token == "-" {
				token = ""
			}
			project := testProject
			if c.project != "" {
				project = c.project
			}
			w := h.do(t, manualBuild(token, project, c.body))
			require.Equal(t, c.status, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), c.says)
			require.Empty(t, h.backend.enqueues())
		})
	}
}

func TestAManualBuildOutsideTheTenancy(t *testing.T) {
	h := newHarness(t, Config{}, servesOnly(t, "github.com/someone-else"))
	h.projects.role = asBuilder
	w := h.do(t, manualBuild(testToken, testProject, buildRequest{Repository: testRepoKey, Ref: "refs/heads/main", Commit: testSHA}))
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Empty(t, h.backend.enqueues())
}

func TestAManualBuildTheBackendRefuses(t *testing.T) {
	h := newHarness(t, Config{})
	h.projects.role = asBuilder
	h.backend.enqueue = func(string, string, string) (string, error) {
		return "", connect.NewError(connect.CodeFailedPrecondition, errors.New("github.com/acme/pay does not hold refs/heads/main at 1111"))
	}
	w := h.do(t, manualBuild(testToken, testProject, buildRequest{Repository: testRepoKey, Ref: "refs/heads/main", Commit: testSHA}))
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, w.Body.String(), "does not hold refs/heads/main")

	h.backend.enqueue = func(string, string, string) (string, error) {
		return "", connect.NewError(connect.CodeUnavailable, errors.New("no backend"))
	}
	w = h.do(t, manualBuild(testToken, testProject, buildRequest{Repository: testRepoKey, Ref: "refs/heads/main", Commit: testSHA}))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

type uploads struct {
	project, repository, ref string
	body                     string
	err                      error
}

func (u *uploads) Put(_ context.Context, projectID, repositoryKey, ref string, bundle io.Reader) (string, error) {
	body, err := io.ReadAll(bundle)
	if err != nil {
		return "", err
	}
	u.project, u.repository, u.ref, u.body = projectID, repositoryKey, ref, string(body)
	if u.err != nil {
		return "", u.err
	}
	return testSHA, nil
}

func upload(token, project, ref, body string) *http.Request {
	return withBody(authed(http.MethodPost, "/upload/"+project+"?ref="+ref, token), []byte(body))
}

func TestACloudTakesNoUpload(t *testing.T) {
	h, _ := registered(t)
	w := h.do(t, upload("tok-"+testUser, testProject, "refs/heads/main", "bundle"))
	require.Equal(t, http.StatusNotFound, w.Code)

	h, _ = registered(t, WithCloudFqdn("tau.example"))
	w = h.do(t, authed(http.MethodGet, "/cloud", "tok-x"))
	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, w.Body.String(), "uploads")
}

func TestAUniverseTakesAnUploadAsACommitOfTheConfiguration(t *testing.T) {
	held := &uploads{}
	h, l := registered(t, WithUploads(held), WithCloudFqdn("tau.example"))
	_, err := l.Register.LinkProject(t.Context(), register.Link{Project: testProject, Config: testRepoKey, Claimant: testUser})
	require.NoError(t, err)

	w := h.do(t, upload("tok-"+testUser, testProject, "refs/heads/main", "the bundle"))
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	require.Equal(t, uploads{project: testProject, repository: testRepoKey, ref: "refs/heads/main", body: "the bundle"}, *held)
	require.Equal(t, []enqueued{{testRepoKey, "refs/heads/main", testSHA}}, h.backend.enqueues())

	w = h.do(t, authed(http.MethodGet, "/cloud", "tok-x"))
	require.Contains(t, w.Body.String(), `"uploads":true`)

	// Somebody who is not a builder on it uploads nothing.
	w = h.do(t, upload("tok-mallory", testProject, "refs/heads/main", "x"))
	require.Equal(t, http.StatusNotFound, w.Code)

	// A ref this cloud does not build is refused before the bundle is read.
	held.body = ""
	w = h.do(t, upload("tok-"+testUser, testProject, "refs/heads/feature", "x"))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.Empty(t, held.body)

	// A code repository of the project is uploaded as itself; any other is
	// refused.
	_, err = l.Register.LinkProject(t.Context(), register.Link{Project: testProject, Config: testRepoKey, Code: []string{"github.com/acme/lib"}, Claimant: testUser})
	require.NoError(t, err)
	w = h.do(t, withBody(authed(http.MethodPost, "/upload/"+testProject+"?ref=refs/heads/main&repository=github.com/acme/lib", "tok-"+testUser), []byte("code")))
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	require.Equal(t, "github.com/acme/lib", held.repository)
	require.Equal(t, enqueued{"github.com/acme/lib", "refs/heads/main", testSHA}, h.backend.enqueues()[1])
	w = h.do(t, withBody(authed(http.MethodPost, "/upload/"+testProject+"?ref=refs/heads/main&repository=github.com/else/lib", "tok-"+testUser), []byte("code")))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// A bundle that cannot be kept is refused with why.
	held.err = errors.New("not a gzip stream")
	w = h.do(t, upload("tok-"+testUser, testProject, "refs/heads/main", "x"))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "not a gzip stream")
	require.Len(t, h.backend.enqueues(), 2)
}

func TestAnUploadForAProjectThatCannotBeRead(t *testing.T) {
	h := newHarness(t, Config{}, WithLinker(unreadable{}), WithUploads(&uploads{}))
	h.projects.role = asBuilder
	w := h.do(t, upload(testToken, testProject, "refs/heads/main", "x"))
	require.Equal(t, http.StatusNotFound, w.Code)
}

type unreadable struct{ Linker }

func (unreadable) ProjectRecord(context.Context, string) (register.ProjectRecord, error) {
	return register.ProjectRecord{}, register.ErrNotLinked
}

func TestUploadsNeedALinker(t *testing.T) {
	_, err := New(Config{}, WithHooks(&fakeHooks{}), WithDeliveries(&fakeDeliveries{}), WithProjects(&fakeProjects{}),
		WithBackend(&fakeBackend{}), WithCallers(&fakeCallers{}), WithUploads(&uploads{}))
	require.ErrorContains(t, err, "linker")

	for _, o := range []Option{WithUploads(nil), WithPublished(nil)} {
		_, err := New(Config{}, o)
		require.Error(t, err)
	}
}

type published map[string]string

func (p published) Environments(_ context.Context, project string) ([]string, error) {
	if project == "unreadable" {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("down"))
	}
	var out []string
	for key := range p {
		if env, ok := strings.CutPrefix(key, project+"/"); ok {
			out = append(out, env)
		}
	}
	return out, nil
}

func (p published) Compiled(_ context.Context, project, environment string) ([]byte, error) {
	got, ok := p[project+"/"+environment]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("never published"))
	}
	return []byte(got), nil
}

func TestAViewerReadsWhatADeploymentRuns(t *testing.T) {
	h := newHarness(t, Config{}, WithPublished(published{testProject + "/prod": `{"object":{}}`}))
	h.projects.role = func(project, _ string) (register.Role, error) { return asBuilder(project, "someone") }

	w := h.do(t, authed(http.MethodGet, "/projects/"+testProject+"/deployments/prod", testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, `{"object":{}}`, w.Body.String())
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))

	w = h.do(t, authed(http.MethodGet, "/projects/"+testProject+"/deployments/preview", testToken))
	require.Equal(t, http.StatusNotFound, w.Code)

	w = h.do(t, authed(http.MethodGet, "/projects/proj-2/deployments/prod", testToken))
	require.Equal(t, http.StatusNotFound, w.Code)

	w = h.do(t, authed(http.MethodGet, "/projects/"+testProject+"/deployments/"+strings.Repeat("e", maxIDLength+1), testToken))
	require.Equal(t, http.StatusNotFound, w.Code)

	w = h.do(t, authed(http.MethodGet, "/projects/"+testProject+"/deployments", testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `["prod"]`, w.Body.String())

	w = h.do(t, authed(http.MethodGet, "/projects/proj-2/deployments", testToken))
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestTheDeploymentsOfAProjectWithNone(t *testing.T) {
	h := newHarness(t, Config{}, WithPublished(published{}))
	h.projects.role = func(project, _ string) (register.Role, error) {
		if project == "unreadable" {
			return register.RoleViewer, nil
		}
		return asBuilder(project, "someone")
	}
	w := h.do(t, authed(http.MethodGet, "/projects/"+testProject+"/deployments", testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `[]`, w.Body.String())

	w = h.do(t, authed(http.MethodGet, "/projects/unreadable/deployments", testToken))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestWithoutPublishedTheRouteDoesNotExist(t *testing.T) {
	h := newHarness(t, Config{})
	w := h.do(t, authed(http.MethodGet, "/projects/"+testProject+"/deployments/prod", testToken))
	require.Equal(t, http.StatusNotFound, w.Code)
}
