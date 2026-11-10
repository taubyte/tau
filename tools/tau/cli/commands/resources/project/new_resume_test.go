package project_test

import (
	"net/http"
	"testing"

	"github.com/h2non/gock"
	"github.com/taubyte/tau/tools/tau/cli"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

const githubAPI = "https://api.github.com"

func exactPath(method, path string, calls *int) gock.MatchFunc {
	return func(r *http.Request, _ *gock.Request) (bool, error) {
		if r.Method != method || r.URL.Path != path {
			return false, nil
		}
		if calls != nil {
			*calls++
		}
		return true, nil
	}
}

func authGet(path string, calls *int, body map[string]any) {
	gock.New(testutil.AuthMockBaseURL).Get("/").AddMatcher(exactPath("GET", path, calls)).Persist().Reply(200).JSON(body)
}

// A clone address that fails at once, without leaving the machine.
func unfinishedProject(id, name string) map[string]any {
	repo := func(n string) map[string]any {
		return map[string]any{"id": n, "name": n, "fullname": "me/" + n, "url": "file:///nonexistent/" + n}
	}
	return map[string]any{"project": map[string]any{"id": id, "name": name,
		"Repositories": map[string]any{"code": repo("code"), "configuration": repo("tb_" + name), "provider": "github"}}}
}

func newProject(t *testing.T, name string) error {
	dir, projectPath, cfg := resourceTestDir(t)
	_, _, err := testutil.RunCLIWithDirAndCwdWithAuthMock(t, cli.Run, dir, projectPath, cfg,
		"--defaults", "--yes", "--color", "never", "new", "project", "--name", name, "--private")
	return err
}

// The project just created is not looked up by listing every project: that
// listing is what timed out and left projects half made. A failure says how
// to finish, and leaves nothing selected.
func TestNewProjectDoesNotListProjectsAfterCreating(t *testing.T) {
	listings, created := 0, 0
	authGet("/projects", &listings, map[string]any{"projects": []any{}})
	authGet("/projects/mockproject123", nil, unfinishedProject("mockproject123", "my_app"))
	gock.New(githubAPI).Post("/user/repos").AddMatcher(exactPath("POST", "/user/repos", &created)).Persist().
		Reply(201).JSON(map[string]any{"id": 7, "name": "r", "full_name": "me/r"})
	defer testutil.ActivateAuthMock()()

	err := newProject(t, "my_app")
	assert.ErrorContains(t, err, "Run `tau new project --name my_app` again")
	assert.Equal(t, created, 2)
	assert.Equal(t, listings, 1, "only the name check may list projects")
	selected, _ := session.Get().SelectedProject()
	assert.Equal(t, selected, "", "a project that failed must not stay selected")
}

// Running the command again picks up a project whose config repository has
// no commits, without creating repositories.
func TestNewProjectFinishesAnUnfinishedProject(t *testing.T) {
	created := 0
	authGet("/projects", nil, map[string]any{"projects": []any{map[string]any{"id": "p1", "name": "my_app"}}})
	authGet("/projects/p1", nil, unfinishedProject("p1", "my_app"))
	gock.New(githubAPI).Get("/repos/me/tb_my_app/commits").Persist().Reply(409).JSON(map[string]any{"message": "Git Repository is empty."})
	gock.New(githubAPI).Post("/user/repos").AddMatcher(exactPath("POST", "/user/repos", &created)).Persist().Reply(500)
	defer testutil.ActivateAuthMock()()

	err := newProject(t, "my_app")
	assert.ErrorContains(t, err, "did not finish")
	assert.Equal(t, created, 0)
}

// A project with commits keeps its name.
func TestNewProjectKeepsAFinishedProjectsName(t *testing.T) {
	authGet("/projects", nil, map[string]any{"projects": []any{map[string]any{"id": "p1", "name": "my_app"}}})
	authGet("/projects/p1", nil, unfinishedProject("p1", "my_app"))
	gock.New(githubAPI).Get("/repos/me/tb_my_app/commits").Persist().Reply(200).JSON([]any{map[string]any{"sha": "abc"}})
	defer testutil.ActivateAuthMock()()

	assert.ErrorContains(t, newProject(t, "my_app"), "already taken")
}
