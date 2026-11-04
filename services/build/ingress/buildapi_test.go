package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/build/artifact"
	"github.com/taubyte/tau/services/build/uploads"
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
	type testCase struct {
		token   string
		project string
		body    any
		role    func(project, user string) (register.Role, error)
		owner   func(key string) (string, error)
		status  int
		says    string
	}
	for name, c := range map[string]testCase{
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

// aBundle is a developer's build of one file.
func aBundle(t *testing.T, content string) []byte {
	t.Helper()
	tree := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tree, "project.yaml"), []byte(content), 0o644))
	var b bytes.Buffer
	require.NoError(t, artifact.PackBundle(&b, tree, []string{"project.yaml"}, nil))
	return b.Bytes()
}

func upload(token, project, ref string, body []byte) *http.Request {
	return withBody(authed(http.MethodPost, "/upload/"+project+"?ref="+ref, token), body)
}

// confirmsNothing is a forge that holds no commit.
type confirmsNothing struct{}

func (confirmsNothing) Confirm(context.Context, string, string, string) error {
	return errors.New("not at the forge")
}

func TestACloudTakesNoUpload(t *testing.T) {
	h, _ := registered(t)
	w := h.do(t, upload("tok-"+testUser, testProject, "refs/heads/main", aBundle(t, "x")))
	require.Equal(t, http.StatusNotFound, w.Code)

	h, _ = registered(t, WithCloudFqdn("tau.example"))
	w = h.do(t, authed(http.MethodGet, "/cloud", "tok-x"))
	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, w.Body.String(), "uploads")
}

func TestAUniverseTakesAnUploadAsACommitOfTheConfiguration(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uploads")
	h, l := registeredWith(t, Config{Uploads: dir}, WithCloudFqdn("tau.example"))
	_, err := l.Register.LinkProject(t.Context(), register.Link{Project: testProject, Config: testRepoKey, Claimant: testUser})
	require.NoError(t, err)

	// THE BACKEND'S HALF reads the same directory.
	held, err := uploads.Open(dir)
	require.NoError(t, err)
	confirm := held.Confirm(confirmsNothing{})

	w := h.do(t, upload("tok-"+testUser, testProject, "refs/heads/main", aBundle(t, "the bundle")))
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	pushed := h.backend.enqueues()
	require.Len(t, pushed, 1)
	require.Equal(t, testRepoKey, pushed[0].repositoryKey)
	require.Equal(t, "refs/heads/main", pushed[0].ref)
	require.NoError(t, confirm.Confirm(t.Context(), testRepoKey, "refs/heads/main", pushed[0].commitSHA),
		"the commit queued is the upload the directory holds")

	w = h.do(t, authed(http.MethodGet, "/cloud", "tok-x"))
	require.Contains(t, w.Body.String(), `"uploads":true`)

	// Somebody who is not a builder on it uploads nothing.
	w = h.do(t, upload("tok-mallory", testProject, "refs/heads/main", aBundle(t, "x")))
	require.Equal(t, http.StatusNotFound, w.Code)

	// A ref this cloud does not build is refused before the bundle is read.
	w = h.do(t, upload("tok-"+testUser, testProject, "refs/heads/feature", aBundle(t, "feature")))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "only the first upload was kept")

	// A code repository of the project is uploaded as itself; any other is
	// refused.
	_, err = l.Register.LinkProject(t.Context(), register.Link{Project: testProject, Config: testRepoKey, Code: []string{"github.com/acme/lib"}, Claimant: testUser})
	require.NoError(t, err)
	w = h.do(t, withBody(authed(http.MethodPost, "/upload/"+testProject+"?ref=refs/heads/main&repository=github.com/acme/lib", "tok-"+testUser), aBundle(t, "code")))
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	code := h.backend.enqueues()[1]
	require.Equal(t, "github.com/acme/lib", code.repositoryKey)
	require.NoError(t, confirm.Confirm(t.Context(), "github.com/acme/lib", "refs/heads/main", code.commitSHA))
	w = h.do(t, withBody(authed(http.MethodPost, "/upload/"+testProject+"?ref=refs/heads/main&repository=github.com/else/lib", "tok-"+testUser), aBundle(t, "code")))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// A bundle that cannot be kept is refused with why.
	w = h.do(t, upload("tok-"+testUser, testProject, "refs/heads/main", []byte("x")))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "not a bundle")
	require.Len(t, h.backend.enqueues(), 2)
}

func TestAnUploadForAProjectThatCannotBeRead(t *testing.T) {
	h := newHarness(t, Config{Uploads: t.TempDir()}, WithLinker(unreadable{}))
	h.projects.role = asBuilder
	w := h.do(t, upload(testToken, testProject, "refs/heads/main", aBundle(t, "x")))
	require.Equal(t, http.StatusNotFound, w.Code)
}

type unreadable struct{ Linker }

func (unreadable) ProjectRecord(context.Context, string) (register.ProjectRecord, error) {
	return register.ProjectRecord{}, register.ErrNotLinked
}

func TestUploadsNeedALinkerAndADirectory(t *testing.T) {
	_, err := New(Config{Uploads: t.TempDir()}, WithHooks(&fakeHooks{}), WithDeliveries(&fakeDeliveries{}), WithProjects(&fakeProjects{}),
		WithBackend(&fakeBackend{}), WithCallers(&fakeCallers{}))
	require.ErrorContains(t, err, "linker")

	file := filepath.Join(t.TempDir(), "a file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	_, err = New(Config{Uploads: filepath.Join(file, "below")}, WithHooks(&fakeHooks{}), WithDeliveries(&fakeDeliveries{}), WithProjects(&fakeProjects{}),
		WithBackend(&fakeBackend{}), WithCallers(&fakeCallers{}), WithLinker(unreadable{}))
	require.ErrorContains(t, err, "uploads")

	_, err = New(Config{}, WithPublished(nil))
	require.Error(t, err)
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
