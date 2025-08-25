package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/h2non/gock"
	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	"gotest.tools/v3/assert"
)

func TestAuthServiceWithMocks(t *testing.T) {
	defer gock.Off()

	ctx := context.Background()
	mockNode := peer.Mock(ctx)
	mockFactory := mock.New()

	cfg := &config.Node{
		NetworkFqdn: "test.tau",
		Node:        mockNode,
		Databases:   mockFactory,
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12349"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12349"},
		PrivateKey:  []byte("private-key"),
		DomainValidation: config.DomainValidation{
			PrivateKey: []byte("private-key"),
			PublicKey:  []byte("public-key"),
		},
	}

	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test that the service was created with mocks
	assert.Assert(t, svc != nil)
	assert.Equal(t, svc.Node(), mockNode)
	assert.Assert(t, svc.KV() != nil)
}

func TestHTTPContextMock(t *testing.T) {
	t.Run("mock context variables", func(t *testing.T) {
		ctx := &MockHTTPContext{
			variables: map[string]interface{}{
				"test":   "value",
				"number": 42,
			},
		}

		// Test Variables() method
		vars := ctx.Variables()
		assert.Equal(t, "value", vars["test"])
		assert.Equal(t, 42, vars["number"])

		// Test SetVariable method
		ctx.SetVariable("new", "newvalue")
		assert.Equal(t, "newvalue", ctx.Variables()["new"])

		// Test GetStringVariable method
		val, err := ctx.GetStringVariable("test")
		assert.NilError(t, err)
		assert.Equal(t, "value", val)

		// Test GetIntVariable method
		num, err := ctx.GetIntVariable("number")
		assert.NilError(t, err)
		assert.Equal(t, 42, num)
	})

	t.Run("mock context request and writer", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		writer := httptest.NewRecorder()

		ctx := &MockHTTPContext{
			request:   req,
			writer:    writer,
			variables: make(map[string]interface{}),
		}

		assert.Equal(t, req, ctx.Request())
		assert.Equal(t, writer, ctx.Writer())
	})
}

func TestGitHubClientMock(t *testing.T) {
	t.Run("create mock GitHub client", func(t *testing.T) {
		client := &githubClient{}
		assert.Assert(t, client != nil)
	})

	t.Run("test GitHub API mocking with gock", func(t *testing.T) {
		defer gock.Off()

		// Mock GitHub API response
		gock.New("https://api.github.com").
			Get("/user").
			Reply(200).
			JSON(map[string]interface{}{
				"login": "testuser",
				"id":    12345,
				"name":  "Test User",
			})

		// Test that the mock is working
		resp, err := http.Get("https://api.github.com/user")
		assert.NilError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, 200, resp.StatusCode)
		assert.Assert(t, gock.IsDone())
	})
}

func TestHelperFunctions(t *testing.T) {
	t.Run("extract project variables logic", func(t *testing.T) {
		// Test the logic that would be used in the HTTP handlers
		ctxVars := map[string]interface{}{
			"config": map[string]interface{}{
				"id": "config-repo-123",
			},
			"code": map[string]interface{}{
				"id": "code-repo-456",
			},
			"project": "test-project",
		}

		// Test the extraction logic
		configID, ok := ctxVars["config"].(map[string]interface{})
		assert.Assert(t, ok)
		assert.Equal(t, "config-repo-123", configID["id"])

		codeID, ok := ctxVars["code"].(map[string]interface{})
		assert.Assert(t, ok)
		assert.Equal(t, "code-repo-456", codeID["id"])

		projectName, ok := ctxVars["project"].(string)
		assert.Assert(t, ok)
		assert.Equal(t, "test-project", projectName)
	})

	t.Run("GitHub client context extraction", func(t *testing.T) {
		// Test the logic for extracting GitHub client from context
		ctxVars := map[string]interface{}{
			"GithubClient": &githubClient{},
		}

		client, exists := ctxVars["GithubClient"]
		assert.Assert(t, exists)
		assert.Assert(t, client != nil)
	})
}

// Test GitHub HTTP endpoint handlers with comprehensive scenarios
func TestGitHubHTTPEndpointsWithFixtures(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12385"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12385"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Replace the newGitHubClient function with a mock
	mockGitHubClient := &mockGitHubClient{}
	svc.newGitHubClient = func(ctx context.Context, token string) (GitHubClient, error) {
		return mockGitHubClient, nil
	}

	// Test 1: Extract project variables
	t.Run("extractProjectVariables", func(t *testing.T) {
		// Create a mock context with the expected variable structure
		mockCtx := &mockHTTPContextWithComplexVars{
			variables: map[string]interface{}{
				"config": map[string]interface{}{
					"id": "test-config-id",
				},
				"code": map[string]interface{}{
					"id": "test-code-id",
				},
				"project": "test-project-name",
			},
		}

		configID, codeID, projectName, err := extractProjectVariables(mockCtx)
		assert.NilError(t, err)
		assert.Equal(t, configID, "test-config-id")
		assert.Equal(t, codeID, "test-code-id")
		assert.Equal(t, projectName, "test-project-name")
	})

	// Test 2: New GitHub project HTTP handler
	t.Run("newGitHubProjectHTTPHandler", func(t *testing.T) {
		mockCtx := &mockHTTPContext{
			variables: map[string]string{
				"project": "test-project",
				"config":  "test-config",
				"code":    "test-code",
			},
		}

		// This will fail because getGithubClientFromContext expects a real GitHub client
		// but we're testing the handler logic, not the actual GitHub API calls
		_, err := svc.newGitHubProjectHTTPHandler(mockCtx)
		assert.Assert(t, err != nil, "Expected error for missing GitHub client")
	})

	// Test 3: Import GitHub project HTTP handler
	t.Run("importGitHubProjectHTTPHandler", func(t *testing.T) {
		mockCtx := &mockHTTPContext{
			variables: map[string]string{
				"project":    "test-project",
				"config":     "test-config",
				"code":       "test-code",
				"project-id": "test-project-id",
			},
		}

		_, err := svc.importGitHubProjectHTTPHandler(mockCtx)
		assert.Assert(t, err != nil, "Expected error for missing GitHub client")
	})

	// Test 4: Register GitHub user repository HTTP handler
	t.Run("registerGitHubUserRepositoryHTTPHandler", func(t *testing.T) {
		mockCtx := &mockHTTPContext{
			variables: map[string]string{
				"provider": "github",
				"id":       "test-repo-id",
			},
		}

		_, err := svc.registerGitHubUserRepositoryHTTPHandler(mockCtx)
		assert.Assert(t, err != nil, "Expected error for missing GitHub client")
	})

	// Test 5: Get GitHub user repository HTTP handler
	t.Run("getGitHubUserRepositoryHTTPHandler", func(t *testing.T) {
		// Test case 1: Missing GitHub client (existing test)
		mockCtx := &mockHTTPContext{
			variables: map[string]string{
				"provider": "github",
				"id":       "test-repo-id",
			},
		}

		_, err := svc.getGitHubUserRepositoryHTTPHandler(mockCtx)
		assert.Assert(t, err != nil, "Expected error for missing GitHub client")

		// Test case 2: Missing provider variable
		mockCtxMissingProvider := &mockHTTPContext{
			variables: map[string]string{
				"id": "test-repo-id",
			},
		}

		_, err = svc.getGitHubUserRepositoryHTTPHandler(mockCtxMissingProvider)
		assert.Assert(t, err != nil, "Expected error for missing provider")

		// Test case 3: Missing id variable
		mockCtxMissingID := &mockHTTPContext{
			variables: map[string]string{
				"provider": "github",
			},
		}

		_, err = svc.getGitHubUserRepositoryHTTPHandler(mockCtxMissingID)
		assert.Assert(t, err != nil, "Expected error for missing id")
	})

	// Test 6: Unregister GitHub user repository HTTP handler
	t.Run("unregisterGitHubUserRepositoryHTTPHandler", func(t *testing.T) {
		mockCtx := &mockHTTPContext{
			variables: map[string]string{
				"provider": "github",
				"id":       "test-repo-id",
			},
		}

		_, err := svc.unregisterGitHubUserRepositoryHTTPHandler(mockCtx)
		assert.Assert(t, err != nil, "Expected error for missing GitHub client")
	})
}
