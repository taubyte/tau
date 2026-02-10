package testutil

import (
	"net/http"
	"strings"

	"github.com/h2non/gock"
)

// BasicConfigForAuthMock returns a config YAML string for flow tests that use the gock auth mock.
// Profile uses token "123456" and type "test" (TestCloud) so the auth client uses auth URL from session file (AuthMockBaseURL).
// projectPath should be the absolute path to the project directory (e.g. filepath.Join(dir, "test_project")).
func BasicConfigForAuthMock(profileName, projectName, projectPath string) string {
	return `profiles:
  ` + profileName + `:
    provider: github
    token: "123456"
    default: true
    git_username: taubyte-test
    git_email: taubytetest@gmail.com
    type: test
    network: sandbox.taubyte.com
projects:
  ` + projectName + `:
    defaultprofile: ` + profileName + `
    location: ` + projectPath + "\n"
}

// AuthMockBaseURL is the fixed base URL for the gock-based auth mock (no random port).
const AuthMockBaseURL = "http://auth.mock"

// ActivateAuthMock registers gock mocks for the auth API used by the CLI (GET /me,
// GET/PUT/DELETE /repository/..., GET /projects, GET /projects/<id>, POST /project/new/<name>,
// etc.) and enables interception. Use with TAUBYTE_URL=AuthMockBaseURL and profile token "123456".
// Returns a cleanup function; call it when the test is done (e.g. defer ActivateAuthMock()()).
func ActivateAuthMock() func() {
	gock.DisableNetworking()
	gock.New(AuthMockBaseURL).
		Get("/me").
		MatchHeader("Authorization", "^github 123456$").
		Persist().
		Reply(200).
		JSON(map[string]interface{}{
			"user": map[string]interface{}{
				"company": "",
				"email":   "",
				"login":   "test_user",
				"name":    "",
			},
		})

	gock.New(AuthMockBaseURL).
		Get("/projects").
		MatchHeader("Authorization", "^github 123456$").
		Persist().
		Reply(200).
		JSON(map[string]interface{}{"projects": []interface{}{}})

	// GET /projects/<id> — return 404 for any project id (tests can add specific mocks if needed)
	matcherProjects := gock.NewEmptyMatcher()
	matcherProjects.Add(gock.MatchMethod)
	matcherProjects.Add(gock.MatchHost)
	matcherProjects.Add(matchPathPrefix("/projects/"))
	gock.New(AuthMockBaseURL).
		SetMatcher(matcherProjects).
		Get("/").
		MatchHeader("Authorization", "^github 123456$").
		Persist().
		Reply(404)

	// POST /project/new/<name>
	matcherNewProject := gock.NewEmptyMatcher()
	matcherNewProject.Add(gock.MatchMethod)
	matcherNewProject.Add(gock.MatchHost)
	matcherNewProject.Add(matchPathPrefix("/project/new/"))
	gock.New(AuthMockBaseURL).
		SetMatcher(matcherNewProject).
		Post("/").
		MatchHeader("Authorization", "^github 123456$").
		Persist().
		Reply(200).
		JSON(map[string]interface{}{
			"project": map[string]interface{}{
				"id":   "mockproject123",
				"name": "mock",
				"Repositories": map[string]interface{}{
					"code":          map[string]interface{}{"id": "1", "name": "code", "fullname": "u/code", "url": "https://github.com/u/code"},
					"configuration": map[string]interface{}{"id": "2", "name": "config", "fullname": "u/config", "url": "https://github.com/u/config"},
					"provider":      "github",
				},
			},
		})

	// GET /repository/<provider>/<id>
	matcherRepo := gock.NewEmptyMatcher()
	matcherRepo.Add(gock.MatchMethod)
	matcherRepo.Add(gock.MatchHost)
	matcherRepo.Add(matchPathPrefix("/repository/"))
	gock.New(AuthMockBaseURL).
		SetMatcher(matcherRepo).
		Get("/").
		MatchHeader("Authorization", "^github 123456$").
		Persist().
		Reply(404).
		JSON(map[string]interface{}{"error": "repository not found"})

	// PUT /repository/<provider>/<id>
	matcherRepoPut := gock.NewEmptyMatcher()
	matcherRepoPut.Add(gock.MatchMethod)
	matcherRepoPut.Add(gock.MatchHost)
	matcherRepoPut.Add(matchPathPrefix("/repository/"))
	gock.New(AuthMockBaseURL).
		SetMatcher(matcherRepoPut).
		Put("/").
		MatchHeader("Authorization", "^github 123456$").
		Persist().
		Reply(200).
		JSON(map[string]interface{}{"key": "repository/github/mock", "code": 200})

	// DELETE /repository/<provider>/<id>
	matcherRepoDel := gock.NewEmptyMatcher()
	matcherRepoDel.Add(gock.MatchMethod)
	matcherRepoDel.Add(gock.MatchHost)
	matcherRepoDel.Add(matchPathPrefix("/repository/"))
	gock.New(AuthMockBaseURL).
		SetMatcher(matcherRepoDel).
		Delete("/").
		MatchHeader("Authorization", "^github 123456$").
		Persist().
		Reply(200).
		JSON(map[string]interface{}{"code": 200})

	gock.Intercept()
	return func() {
		gock.Off()
		gock.EnableNetworking()
	}
}

// matchPathPrefix returns a gock MatchFunc that matches when the request path has the given prefix.
func matchPathPrefix(prefix string) gock.MatchFunc {
	return func(req *http.Request, ereq *gock.Request) (bool, error) {
		return strings.HasPrefix(req.URL.Path, prefix), nil
	}
}
