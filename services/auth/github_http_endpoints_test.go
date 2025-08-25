package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/h2non/gock"
	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	"gotest.tools/v3/assert"
)

// MockHTTPContext implements the http.Context interface for testing
type MockHTTPContext struct {
	request   *http.Request
	writer    http.ResponseWriter
	variables map[string]interface{}
	body      []byte
}

func (m *MockHTTPContext) HandleWith(handler http.Handler) error    { return nil }
func (m *MockHTTPContext) HandleAuth(handler http.Handler) error    { return nil }
func (m *MockHTTPContext) HandleCleanup(handler http.Handler) error { return nil }
func (m *MockHTTPContext) Request() *http.Request                   { return m.request }
func (m *MockHTTPContext) Writer() http.ResponseWriter              { return m.writer }
func (m *MockHTTPContext) ParseBody(obj interface{}) error          { return nil }
func (m *MockHTTPContext) RawResponse() bool                        { return false }
func (m *MockHTTPContext) SetRawResponse(val bool)                  {}
func (m *MockHTTPContext) Variables() map[string]interface{}        { return m.variables }
func (m *MockHTTPContext) SetVariable(key string, val interface{})  { m.variables[key] = val }
func (m *MockHTTPContext) Body() []byte                             { return m.body }
func (m *MockHTTPContext) SetBody(body []byte)                      { m.body = body }
func (m *MockHTTPContext) GetStringVariable(key string) (string, error) {
	if v, ok := m.variables[key].(string); ok {
		return v, nil
	}
	return "", fmt.Errorf("variable not found or not a string")
}
func (m *MockHTTPContext) GetStringArrayVariable(key string) ([]string, error) {
	if v, ok := m.variables[key].([]string); ok {
		return v, nil
	}
	return nil, fmt.Errorf("variable not found or not a string array")
}
func (m *MockHTTPContext) GetStringMapVariable(key string) (map[string]interface{}, error) {
	if v, ok := m.variables[key].(map[string]interface{}); ok {
		return v, nil
	}
	return nil, fmt.Errorf("variable not found or not a string map")
}
func (m *MockHTTPContext) GetIntVariable(key string) (int, error) {
	if v, ok := m.variables[key].(int); ok {
		return v, nil
	}
	return 0, fmt.Errorf("variable not found or not an int")
}

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
