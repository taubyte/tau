package auth

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/google/go-github/v71/github"
	"github.com/taubyte/tau/core/services/tns"
	httppkg "github.com/taubyte/tau/pkg/http"
)

// Mock HTTP context for testing
type mockHTTPContext struct {
	variables map[string]string
	body      []byte
}

func (m *mockHTTPContext) GetStringVariable(key string) (string, error) {
	if val, exists := m.variables[key]; exists {
		return val, nil
	}
	return "", fmt.Errorf("variable %s not found", key)
}

func (m *mockHTTPContext) GetVariable(key string) (interface{}, error) {
	if val, exists := m.variables[key]; exists {
		return val, nil
	}
	return nil, fmt.Errorf("variable %s not found", key)
}

func (m *mockHTTPContext) Body() []byte {
	return m.body
}

func (m *mockHTTPContext) SetBody(body []byte) {
	m.body = body
}

func (m *mockHTTPContext) Variables() map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m.variables {
		result[k] = v
	}
	return result
}

func (m *mockHTTPContext) SetVariable(key string, val interface{}) {
	if strVal, ok := val.(string); ok {
		m.variables[key] = strVal
	}
}

func (m *mockHTTPContext) Request() *http.Request {
	// Create a minimal mock request with context
	req, _ := http.NewRequest("GET", "/test", nil)
	return req
}

func (m *mockHTTPContext) Writer() http.ResponseWriter {
	return nil
}

func (m *mockHTTPContext) ParseBody(obj interface{}) error {
	return nil
}

func (m *mockHTTPContext) RawResponse() bool {
	return false
}

func (m *mockHTTPContext) SetRawResponse(val bool) {
}

func (m *mockHTTPContext) HandleWith(handler httppkg.Handler) error {
	return nil
}

func (m *mockHTTPContext) HandleAuth(handler httppkg.Handler) error {
	return nil
}

func (m *mockHTTPContext) HandleCleanup(handler httppkg.Handler) error {
	return nil
}

func (m *mockHTTPContext) GetStringArrayVariable(key string) ([]string, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockHTTPContext) GetStringMapVariable(key string) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockHTTPContext) GetIntVariable(key string) (int, error) {
	return 0, fmt.Errorf("not implemented")
}

// Mock authorization for testing
type mockAuth struct {
	Type  string
	Token string
}

// Mock HTTP context with authorization support
type mockHTTPContextWithAuth struct {
	*mockHTTPContext
	auth *mockAuth
}

func (m *mockHTTPContextWithAuth) GetVariable(key string) (interface{}, error) {
	if key == "Authorization" {
		return m.auth, nil
	}
	return m.mockHTTPContext.GetVariable(key)
}

func (m *mockHTTPContextWithAuth) SetVariable(key string, val interface{}) {
	if key == "Authorization" {
		if auth, ok := val.(*mockAuth); ok {
			m.auth = auth
		}
		return
	}
	m.mockHTTPContext.SetVariable(key, val)
}

// Mock TNS client for testing
type mockTNSClient struct {
	tns.Client
}

func (m *mockTNSClient) Push(path []string, data interface{}) error {
	// Mock successful push
	return nil
}

func (m *mockTNSClient) Close() {
	// Mock successful close
}

// Mock HTTP context with complex variables for testing extractProjectVariables
type mockHTTPContextWithComplexVars struct {
	variables map[string]interface{}
}

func (m *mockHTTPContextWithComplexVars) Variables() map[string]interface{} {
	return m.variables
}

func (m *mockHTTPContextWithComplexVars) GetStringVariable(key string) (string, error) {
	if val, exists := m.variables[key]; exists {
		if strVal, ok := val.(string); ok {
			return strVal, nil
		}
	}
	return "", fmt.Errorf("variable %s not found or not string", key)
}

func (m *mockHTTPContextWithComplexVars) GetVariable(key string) (interface{}, error) {
	if val, exists := m.variables[key]; exists {
		return val, nil
	}
	return nil, fmt.Errorf("variable %s not found", key)
}

func (m *mockHTTPContextWithComplexVars) Body() []byte {
	return nil
}

func (m *mockHTTPContextWithComplexVars) SetBody(body []byte) {}

func (m *mockHTTPContextWithComplexVars) SetVariable(key string, val interface{}) {
	m.variables[key] = val
}

func (m *mockHTTPContextWithComplexVars) Request() *http.Request {
	req, _ := http.NewRequest("GET", "/test", nil)
	return req
}

func (m *mockHTTPContextWithComplexVars) Writer() http.ResponseWriter {
	return nil
}

func (m *mockHTTPContextWithComplexVars) ParseBody(obj interface{}) error {
	return nil
}

func (m *mockHTTPContextWithComplexVars) RawResponse() bool {
	return false
}

func (m *mockHTTPContextWithComplexVars) SetRawResponse(val bool) {}

func (m *mockHTTPContextWithComplexVars) HandleWith(handler httppkg.Handler) error {
	return nil
}

func (m *mockHTTPContextWithComplexVars) HandleAuth(handler httppkg.Handler) error {
	return nil
}

func (m *mockHTTPContextWithComplexVars) HandleCleanup(handler httppkg.Handler) error {
	return nil
}

func (m *mockHTTPContextWithComplexVars) GetStringArrayVariable(key string) ([]string, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockHTTPContextWithComplexVars) GetStringMapVariable(key string) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockHTTPContextWithComplexVars) GetIntVariable(key string) (int, error) {
	return 0, fmt.Errorf("not implemented")
}

// Mock GitHub client for testing
type mockGitHubClient struct {
	currentRepo *github.Repository
	user        *github.User
}

func (m *mockGitHubClient) Cur() *github.Repository {
	return nil
}

func (m *mockGitHubClient) Me() *github.User {
	if m.user == nil {
		// Create a mock user if none exists
		m.user = &github.User{
			ID:    github.Int64(12345),
			Login: github.Ptr("testuser"),
		}
	}
	return m.user
}

func (m *mockGitHubClient) GetByID(id string) error {
	// Create a mock repository when GetByID is called
	m.currentRepo = &github.Repository{
		ID:       github.Int64(12345),
		SSHURL:   github.Ptr("git@github.com:test/test-repo.git"),
		FullName: github.Ptr("test/test-repo"),
	}
	return nil
}

func (m *mockGitHubClient) GetCurrentRepository() (*github.Repository, error) {
	if m.currentRepo == nil {
		return nil, errors.New("no current repository")
	}
	return m.currentRepo, nil
}

func (m *mockGitHubClient) CreateRepository(name *string, description *string, private *bool) error {
	return nil
}

func (m *mockGitHubClient) CreateDeployKey(name *string, key *string) error {
	return nil
}

func (m *mockGitHubClient) CreatePushHook(name *string, url *string, devMode bool) (int64, string, error) {
	return 0, "", nil
}

func (m *mockGitHubClient) ListMyRepos() map[string]RepositoryBasicInfo {
	return make(map[string]RepositoryBasicInfo)
}

func (m *mockGitHubClient) ShortRepositoryInfo(id string) RepositoryShortInfo {
	return RepositoryShortInfo{}
}
