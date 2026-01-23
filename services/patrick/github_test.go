package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/dream/helpers"
	httpPkg "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	servicesCommon "github.com/taubyte/tau/services/common"
	"gotest.tools/v3/assert"
)

func generateHMAC(body []byte, secret string) string {
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

type testSetup struct {
	ctx        *mockHTTPContext
	authClient *mockAuthClient
	tnsClient  *mockTNSClient
	mockDB     *mock.KVDB
	mockNode   *mockNode
	service    *PatrickService
}

func createTestSetup(devMode bool) *testSetup {
	ctx := newMockHTTPContext()

	authClient := &mockAuthClient{
		repos: map[int]mockRepo{
			12345: {projectID: "project-456"},
		},
		hooks: mockHooks{hooks: make(map[string]mockAuthHook)},
	}

	tnsClient := &mockTNSClient{
		lookupResponse: []string{"repositories/github/12345/extra/project-456"},
	}

	mockFactory := mock.New()
	mockDB, _ := mockFactory.New(nil, "/test", 0)

	mockNode := &mockNode{}

	service := &PatrickService{
		authClient: authClient,
		tnsClient:  tnsClient,
		db:         mockDB,
		node:       mockNode,
		devMode:    devMode,
	}

	return &testSetup{
		ctx:        ctx,
		authClient: authClient,
		tnsClient:  tnsClient,
		mockDB:     mockDB.(*mock.KVDB),
		mockNode:   mockNode,
		service:    service,
	}
}

func (ts *testSetup) setupGitHubWebhook(body []byte, secret string) {
	ts.ctx.SetHeaders(map[string]string{
		"X-GitHub-Event":    "push",
		"X-GitHub-Delivery": "test-delivery-id",
		"X-Hub-Signature":   "sha1=" + generateHMAC(body, secret),
		"Content-Type":      "application/json",
	})
	ts.ctx.SetBody(body)
}

func (ts *testSetup) setupHookInAuth(hookID, secret string) {
	ts.authClient.hooks.hooks[hookID] = mockAuthHook{secret: secret}
	ts.ctx.SetVariable("hook", hookID)
}

func (ts *testSetup) setupSecretInContext(secret string) {
	ts.ctx.SetVariables(map[string]interface{}{
		"GithubSecret": secret,
	})
}

func createGitHubTestJob(id string) *patrick.Job {
	return &patrick.Job{
		Id:     id,
		Status: patrick.JobStatusOpen,
		Meta: patrick.Meta{
			Repository: patrick.Repository{
				Provider: "github",
				ID:       12345,
				SSHURL:   "git@github.com:test/repo.git",
				Branch:   "main",
			},
		},
	}
}

func assertJobResult(t *testing.T, result interface{}) {
	job, ok := result.(*patrick.Job)
	assert.Assert(t, ok, "Result should be a Job")
	assert.Assert(t, job.Id != "", "Job should have an ID")
	assert.Equal(t, job.Status, patrick.JobStatusOpen)
	assert.Equal(t, job.Meta.Repository.Provider, "github")
}

type mockHTTPContext struct {
	request   *http.Request
	writer    http.ResponseWriter
	variables map[string]interface{}
	body      []byte
	headers   map[string]string
}

func newMockHTTPContext() *mockHTTPContext {
	return &mockHTTPContext{
		variables: make(map[string]interface{}),
		headers:   make(map[string]string),
	}
}

func (m *mockHTTPContext) SetRequest(req *http.Request) {
	m.request = req
}

func (m *mockHTTPContext) SetWriter(w http.ResponseWriter) {
	m.writer = w
}

func (m *mockHTTPContext) SetVariables(vars map[string]interface{}) {
	for k, v := range vars {
		m.variables[k] = v
	}
}

func (m *mockHTTPContext) SetBody(body []byte) {
	m.body = body
}

func (m *mockHTTPContext) SetHeader(key, value string) {
	m.headers[key] = value
}

func (m *mockHTTPContext) SetHeaders(headers map[string]string) {
	for k, v := range headers {
		m.headers[k] = v
	}
}

func (m *mockHTTPContext) HandleWith(handler httpPkg.Handler) error    { return nil }
func (m *mockHTTPContext) HandleAuth(handler httpPkg.Handler) error    { return nil }
func (m *mockHTTPContext) HandleCleanup(handler httpPkg.Handler) error { return nil }
func (m *mockHTTPContext) Request() *http.Request {
	if m.request != nil {
		return m.request
	}
	req, _ := http.NewRequest("POST", "http://localhost:8080/github", bytes.NewReader(m.body))
	for k, v := range m.headers {
		req.Header.Set(k, v)
	}
	return req
}
func (m *mockHTTPContext) Writer() http.ResponseWriter             { return m.writer }
func (m *mockHTTPContext) ParseBody(obj interface{}) error         { return nil }
func (m *mockHTTPContext) RawResponse() bool                       { return false }
func (m *mockHTTPContext) SetRawResponse(val bool)                 {}
func (m *mockHTTPContext) Variables() map[string]interface{}       { return m.variables }
func (m *mockHTTPContext) SetVariable(key string, val interface{}) { m.variables[key] = val }
func (m *mockHTTPContext) Body() []byte                            { return m.body }

func (m *mockHTTPContext) GetStringVariable(key string) (string, error) {
	if v, ok := m.variables[key].(string); ok {
		return v, nil
	}
	return "", fmt.Errorf("variable not found or not a string")
}

func (m *mockHTTPContext) GetStringArrayVariable(key string) ([]string, error) {
	if v, ok := m.variables[key].([]string); ok {
		return v, nil
	}
	return nil, fmt.Errorf("variable not found or not a string array")
}

func (m *mockHTTPContext) GetStringMapVariable(key string) (map[string]interface{}, error) {
	if v, ok := m.variables[key].(map[string]interface{}); ok {
		return v, nil
	}
	return nil, fmt.Errorf("variable not found or not a string map")
}

func (m *mockHTTPContext) GetIntVariable(key string) (int, error) {
	if v, ok := m.variables[key].(int); ok {
		return v, nil
	}
	return 0, fmt.Errorf("variable not found or not an int")
}

func TestRegisterJob(t *testing.T) {
	tests := []struct {
		name          string
		job           *patrick.Job
		setupMocks    func(*testSetup)
		expectedError string
	}{
		{
			name: "successful registration",
			job:  createGitHubTestJob("test-job-1"),
			setupMocks: func(ts *testSetup) {
				// TNS is already set up to return valid project path
			},
			expectedError: "",
		},
		{
			name: "database put error",
			job:  createGitHubTestJob("test-job-2"),
			setupMocks: func(ts *testSetup) {
				ts.mockDB.Close() // This will cause put to fail
			},
			expectedError: "failed putting job into database with error",
		},
		{
			name: "pubsub error",
			job:  createGitHubTestJob("test-job-3"),
			setupMocks: func(ts *testSetup) {
				ts.mockNode.pubsubError = errors.New("pubsub failed")
			},
			expectedError: "failed to send over pubsub error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := createTestSetup(false)
			tt.setupMocks(ts)

			err := ts.service.RegisterJob(context.Background(), tt.job)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestGetHook(t *testing.T) {
	tests := []struct {
		name          string
		hookID        string
		setupMocks    func(*testSetup)
		expectedError string
	}{
		{
			name:   "successful hook retrieval",
			hookID: "test-hook-1",
			setupMocks: func(ts *testSetup) {
				ts.authClient.hooks.hooks["test-hook-1"] = mockAuthHook{secret: "test-secret"}
			},
			expectedError: "",
		},
		{
			name:   "hook not found",
			hookID: "nonexistent-hook",
			setupMocks: func(ts *testSetup) {
				// Mock will return error for nonexistent hook
			},
			expectedError: "hook not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := createTestSetup(false)
			tt.setupMocks(ts)

			hook, err := ts.service.getHook(tt.hookID)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				assert.Assert(t, hook == nil)
			} else {
				assert.NilError(t, err)
				assert.Assert(t, hook != nil)
			}
		})
	}
}

func TestGithubCheckHookAndExtractSecret(t *testing.T) {
	tests := []struct {
		name          string
		devMode       bool
		setupMocks    func(*testSetup)
		expectedError string
	}{
		{
			name:    "successful hook extraction",
			devMode: false,
			setupMocks: func(ts *testSetup) {
				ts.ctx.SetVariable("hook", "test-hook-1")
				ts.authClient.hooks.hooks["test-hook-1"] = mockAuthHook{secret: "test-secret"}
			},
			expectedError: "",
		},
		{
			name:    "get string variable error",
			devMode: false,
			setupMocks: func(ts *testSetup) {
				// Context will return error for GetStringVariable
				ts.ctx.variables = map[string]interface{}{}
			},
			expectedError: "get string context failed",
		},
		{
			name:    "get hook error",
			devMode: false,
			setupMocks: func(ts *testSetup) {
				ts.ctx.SetVariable("hook", "nonexistent-hook")
				// Auth client will return error for nonexistent hook
			},
			expectedError: "get hook failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := createTestSetup(tt.devMode)
			tt.setupMocks(ts)

			result, err := ts.service.githubCheckHookAndExtractSecret(ts.ctx)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				assert.Assert(t, result == nil)
			} else {
				assert.NilError(t, err)
				assert.Assert(t, result == nil)
				// Verify that the secret was set in the context
				secret, exists := ts.ctx.variables["GithubSecret"]
				assert.Assert(t, exists, "GithubSecret should be set in context")
				assert.Equal(t, secret, "test-secret")
			}
		})
	}
}

func TestGithubHookHandler(t *testing.T) {
	tests := []struct {
		name          string
		devMode       bool
		delayJob      bool
		secret        string
		body          []byte
		setupMocks    func(*testSetup)
		expectedError string
	}{
		{
			name:     "successful config payload processing",
			devMode:  true, // Use dev mode to bypass HMAC verification
			delayJob: false,
			secret:   "test-secret",
			body:     helpers.ConfigPayload,
			setupMocks: func(ts *testSetup) {
				ts.setupHookInAuth("test-hook-1", "taubyte_secret")
				ts.tnsClient.pushError = nil
			},
			expectedError: "",
		},
		{
			name:     "successful code payload processing",
			devMode:  true, // Use dev mode to bypass HMAC verification
			delayJob: false,
			secret:   "test-secret",
			body:     helpers.CodePayload,
			setupMocks: func(ts *testSetup) {
				ts.setupHookInAuth("test-hook-1", "taubyte_secret")
				ts.tnsClient.pushError = nil
			},
			expectedError: "",
		},
		{
			name:     "successful website payload processing",
			devMode:  true, // Use dev mode to bypass HMAC verification
			delayJob: false,
			secret:   "test-secret",
			body:     helpers.WebsitePayload,
			setupMocks: func(ts *testSetup) {
				ts.setupHookInAuth("test-hook-1", "taubyte_secret")
				ts.tnsClient.pushError = nil
			},
			expectedError: "",
		},
		{
			name:     "dev mode with any payload",
			devMode:  true,
			delayJob: false,
			secret:   "test-secret",
			body:     helpers.ConfigPayload,
			setupMocks: func(ts *testSetup) {
				ts.setupHookInAuth("test-hook-1", "taubyte_secret")
				ts.tnsClient.pushError = nil
			},
			expectedError: "",
		},
		{
			name:     "missing github secret",
			devMode:  false,
			delayJob: false,
			secret:   "",
			body:     helpers.ConfigPayload,
			setupMocks: func(ts *testSetup) {
				// No GithubSecret set
			},
			expectedError: "variable not found or not a string",
		},
		{
			name:     "invalid payload",
			devMode:  false,
			delayJob: false,
			secret:   "test-secret",
			body:     []byte("invalid json"),
			setupMocks: func(ts *testSetup) {
				ts.setupSecretInContext("test-secret")
			},
			expectedError: "parsing hook failed",
		},
		{
			name:     "tns push error",
			devMode:  false,
			delayJob: false,
			secret:   "test-secret",
			body:     helpers.ConfigPayload,
			setupMocks: func(ts *testSetup) {
				ts.setupHookInAuth("test-hook-1", "taubyte_secret")
				ts.tnsClient.pushError = errors.New("tns push failed")
			},
			expectedError: "failed registering new job repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Enable fake secret for testing
			servicesCommon.FakeSecret = true
			t.Logf("FakeSecret: %v, devMode: %v", servicesCommon.FakeSecret, tt.devMode)

			ts := createTestSetup(tt.devMode)
			ts.setupGitHubWebhook(tt.body, "taubyte_secret")
			tt.setupMocks(ts)

			// Call githubCheckHookAndExtractSecret first to set the secret (for all cases that need HMAC verification)
			if tt.expectedError == "" || tt.name == "tns push error" {
				t.Logf("Calling githubCheckHookAndExtractSecret with FakeSecret=%v, devMode=%v", servicesCommon.FakeSecret, ts.service.devMode)
				_, err := ts.service.githubCheckHookAndExtractSecret(ts.ctx)
				if err != nil {
					t.Fatalf("Failed to extract secret: %v", err)
				}
				t.Logf("Secret extracted successfully")
			}

			result, err := ts.service.githubHookHandler(ts.ctx)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				assert.Assert(t, result == nil)
			} else {
				assert.NilError(t, err)
				assertJobResult(t, result)
			}
		})
	}
}
