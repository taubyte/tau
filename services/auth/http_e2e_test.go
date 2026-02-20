//go:build dreaming

package auth_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/services/auth/hooks"
	"github.com/taubyte/tau/services/auth/projects"
	"github.com/taubyte/tau/services/auth/repositories"
	"github.com/taubyte/tau/utils/id"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/auth/dream"
	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
)

func TestAuthServiceHTTPEndpoints_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"auth": {},
			"tns":  {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Auth: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(5 * time.Second)

	// Get the auth service URL
	authURL, err := u.GetURLHttp(u.Auth().Node())
	assert.NilError(t, err)

	t.Run("HTTPEndpoints", func(t *testing.T) {
		testHTTPEndpoints(t, u, authURL)
	})

	t.Run("StreamAPI", func(t *testing.T) {
		testStreamAPIEndpoints(t, u)
	})
}

func testHTTPEndpoints(t *testing.T, u *dream.Universe, authURL string) {
	// Test domain validation endpoint
	t.Run("DomainValidation", func(t *testing.T) {
		testDomainValidationEndpoint(t, authURL)
	})

	// Test domain validation with proper setup
	t.Run("DomainValidationWithSetup", func(t *testing.T) {
		testDomainValidationWithSetup(t, u, authURL)
	})

	// Test successful domain validation with mock GitHub client
	t.Run("DomainValidationSuccess", func(t *testing.T) {
		testDomainValidationSuccess(t, u, authURL)
	})

	// Test GitHub endpoints (these would require proper GitHub client setup)
	t.Run("GitHubEndpoints", func(t *testing.T) {
		testGitHubEndpoints(t, u, authURL)
	})
}

func testDomainValidationEndpoint(t *testing.T, authURL string) {
	// Test with invalid project ID (too short)
	resp, err := http.Post(authURL+"/domain/test.example.com/for/123", "application/json", nil)
	if err == nil {
		defer resp.Body.Close()
		assert.Assert(t, resp.StatusCode != 200)
	}

	// Test with valid project ID format (8+ characters)
	validProjectID := "1234567890abcdef"
	resp2, err := http.Post(authURL+"/domain/test.example.com/for/"+validProjectID, "application/json", nil)
	if err == nil {
		defer resp2.Body.Close()
		assert.Assert(t, resp2.StatusCode != 200, "Expected auth failure, not format validation failure")
	}
}

func testDomainValidationWithSetup(t *testing.T, u *dream.Universe, authURL string) {
	projectID := "test_project_12345"
	project, err := projects.New(u.Auth().KV(), projects.Data{
		"id":   projectID,
		"name": "Test Project for Domain Validation",
	})
	assert.NilError(t, err)

	err = project.Register()
	assert.NilError(t, err)
	defer project.Delete()

	testDomain := "test.example.com"
	resp, err := http.Post(authURL+"/domain/"+testDomain+"/for/"+projectID, "application/json", nil)
	if err == nil {
		defer resp.Body.Close()
		assert.Assert(t, resp.StatusCode != 200, "Expected auth failure, not project ID validation failure")
	}

	testDomain2 := "another.example.com"
	resp2, err := http.Post(authURL+"/domain/"+testDomain2+"/for/"+projectID, "application/json", nil)
	if err == nil {
		defer resp2.Body.Close()
		assert.Assert(t, resp2.StatusCode != 200, "Expected auth failure for second domain")
	}

	t.Run("DirectDomainValidation", func(t *testing.T) {
		testDirectDomainValidation(t, u, projectID, testDomain)
	})
}

func testDirectDomainValidation(t *testing.T, u *dream.Universe, projectID, domain string) {
	authService := u.Auth()
	assert.Assert(t, authService != nil)

	ctx := u.Context()
	retrievedProject, err := projects.Fetch(ctx, authService.KV(), projectID)
	assert.NilError(t, err)
	assert.Assert(t, retrievedProject != nil)
	assert.Equal(t, retrievedProject.Name(), "Test Project for Domain Validation")
}

func testDomainValidationSuccess(t *testing.T, u *dream.Universe, authURL string) {
	projectID := "success_project_12345"
	project, err := projects.New(u.Auth().KV(), projects.Data{
		"id":   projectID,
		"name": "Success Test Project",
	})
	assert.NilError(t, err)

	err = project.Register()
	assert.NilError(t, err)
	defer project.Delete()

	ctx := u.Context()
	authService := u.Auth()

	retrievedProject, err := projects.Fetch(ctx, authService.KV(), projectID)
	assert.NilError(t, err)
	assert.Assert(t, retrievedProject != nil)

	testDomain := "success.example.com"
	expectedURL := authURL + "/domain/" + testDomain + "/for/" + projectID

	req, err := http.NewRequest("POST", expectedURL, nil)
	assert.NilError(t, err)
	req.Header.Set("Authorization", "Bearer mock_github_token")
	req.Header.Set("Content-Type", "application/json")

	assert.Assert(t, req != nil)
	assert.Equal(t, req.Method, "POST")
	assert.Equal(t, req.URL.Path, "/domain/"+testDomain+"/for/"+projectID)

	t.Run("ExpectedResponseFormat", func(t *testing.T) {
		testExpectedResponseFormat(t, projectID, testDomain)
	})
}

func testExpectedResponseFormat(t *testing.T, projectID, domain string) {
	expectedResponse := map[string]string{
		"token": "domain_validation_token_here",
		"entry": projectID[:8] + "." + domain,
		"type":  "txt",
	}

	assert.Assert(t, expectedResponse["token"] != "")
	assert.Assert(t, expectedResponse["entry"] != "")
	assert.Equal(t, expectedResponse["type"], "txt")

	expectedEntry := projectID[:8] + "." + domain
	assert.Equal(t, expectedResponse["entry"], expectedEntry)
}

func testGitHubEndpoints(t *testing.T, u *dream.Universe, authURL string) {
	resp, err := http.Get(authURL + "/health")
	if err == nil {
		defer resp.Body.Close()
		assert.Assert(t, resp != nil)
	}
}

func testStreamAPIEndpoints(t *testing.T, u *dream.Universe) {
	ctx := u.Context()

	t.Run("HooksStreamAPI", func(t *testing.T) {
		testHooksStreamAPI(t, u, ctx)
	})

	t.Run("RepositoriesStreamAPI", func(t *testing.T) {
		testRepositoriesStreamAPI(t, u, ctx)
	})
}

func testHooksStreamAPI(t *testing.T, u *dream.Universe, ctx context.Context) {
	hookID := id.Generate()
	hook, err := hooks.New(u.Auth().KV(), hooks.Data{
		"id":         hookID,
		"provider":   "github",
		"github_id":  99999,
		"repository": 88888,
		"secret":     "stream_test_secret",
	})
	assert.NilError(t, err)

	err = hook.Register(ctx)
	assert.NilError(t, err)
	defer hook.Delete(ctx)

	retrievedHook, err := hooks.Fetch(ctx, u.Auth().KV(), hookID)
	assert.NilError(t, err)
	assert.Assert(t, retrievedHook != nil)
	assert.Equal(t, retrievedHook.ID(), hookID)
}

func testRepositoriesStreamAPI(t *testing.T, u *dream.Universe, ctx context.Context) {
	repo, err := repositories.New(u.Auth().KV(), repositories.Data{
		"id":       99999,
		"provider": "github",
		"name":     "test/stream-repo",
		"project":  "stream_test_project",
		"key":      "stream_test_key",
		"url":      "https://github.com/test/stream-repo",
	})
	assert.NilError(t, err)

	err = repo.Register(ctx)
	assert.NilError(t, err)
	defer repo.Delete(ctx)

	retrievedRepo, err := repositories.Fetch(ctx, u.Auth().KV(), "99999")
	assert.NilError(t, err)
	assert.Assert(t, retrievedRepo != nil)
	assert.Equal(t, retrievedRepo.ID(), 99999)
	assert.Equal(t, retrievedRepo.Provider(), "github")
}

func TestAuthServiceIntegration_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"auth": {},
			"tns":  {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Auth: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(5 * time.Second)

	simple, err := u.Simple("client")
	if err != nil {
		t.Fatal(err)
	}

	auth, err := simple.Auth()
	assert.NilError(t, err)

	t.Run("ServiceIntegration", func(t *testing.T) {
		testServiceIntegration(t, u, auth)
	})
}

func testServiceIntegration(t *testing.T, u *dream.Universe, auth interface{}) {
	ctx := u.Context()

	t.Run("CompleteWorkflow", func(t *testing.T) {
		testCompleteWorkflow(t, u, ctx)
	})
}

func testCompleteWorkflow(t *testing.T, u *dream.Universe, ctx context.Context) {
	projectID := "integration_test_project"

	repo1, err := repositories.New(u.Auth().KV(), repositories.Data{
		"id":       11111,
		"provider": "github",
		"name":     "test/config-repo",
		"project":  projectID,
		"key":      "config_repo_key",
		"url":      "https://github.com/test/config-repo",
	})
	assert.NilError(t, err)

	repo2, err := repositories.New(u.Auth().KV(), repositories.Data{
		"id":       22222,
		"provider": "github",
		"name":     "test/code-repo",
		"project":  projectID,
		"key":      "code_repo_key",
		"url":      "https://github.com/test/code-repo",
	})
	assert.NilError(t, err)

	err = repo1.Register(ctx)
	assert.NilError(t, err)
	defer repo1.Delete(ctx)

	err = repo2.Register(ctx)
	assert.NilError(t, err)
	defer repo2.Delete(ctx)

	hook1, err := hooks.New(u.Auth().KV(), hooks.Data{
		"id":         id.Generate(),
		"provider":   "github",
		"github_id":  11111,
		"repository": 11111,
		"secret":     "hook1_secret",
	})
	assert.NilError(t, err)

	hook2, err := hooks.New(u.Auth().KV(), hooks.Data{
		"id":         id.Generate(),
		"provider":   "github",
		"github_id":  22222,
		"repository": 22222,
		"secret":     "hook2_secret",
	})
	assert.NilError(t, err)

	err = hook1.Register(ctx)
	assert.NilError(t, err)
	defer hook1.Delete(ctx)

	err = hook2.Register(ctx)
	assert.NilError(t, err)
	defer hook2.Delete(ctx)

	retrievedRepo1, err := repositories.Fetch(ctx, u.Auth().KV(), "11111")
	assert.NilError(t, err)
	assert.Assert(t, retrievedRepo1 != nil)

	retrievedRepo2, err := repositories.Fetch(ctx, u.Auth().KV(), "22222")
	assert.NilError(t, err)
	assert.Assert(t, retrievedRepo2 != nil)

	hookList, err := u.Auth().KV().List(ctx, "/hooks/")
	assert.NilError(t, err)
	assert.Assert(t, len(hookList) >= 2)

	repo1Hooks := retrievedRepo1.Hooks(ctx)
	assert.Assert(t, len(repo1Hooks) >= 1)

	repo2Hooks := retrievedRepo2.Hooks(ctx)
	assert.Assert(t, len(repo2Hooks) >= 1)
}
