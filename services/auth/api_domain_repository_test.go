package auth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"testing"

	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/keypair"
	httpPkg "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	"github.com/taubyte/tau/utils/id"
	"gotest.tools/v3/assert"
)

// Test configuration and setup helpers
type testConfig struct {
	port     int
	withKeys bool
}

func createTestService(t *testing.T, cfg testConfig) (*AuthService, func()) {
	ctx := context.Background()
	mockFactory := mock.New()

	svcConfig := &config.Node{
		P2PListen:   []string{fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", cfg.port)},
		P2PAnnounce: []string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", cfg.port)},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}

	if cfg.withKeys {
		privKey, pubKey := generateTestKeys(t)
		svcConfig.DomainValidation = config.DomainValidation{
			PrivateKey: privKey,
			PublicKey:  pubKey,
		}
	}

	svc, err := New(ctx, svcConfig)
	assert.NilError(t, err)

	// Replace TNS client with mock to avoid timeouts
	svc.tnsClient = &mockTNSClient{}

	cleanup := func() {
		svc.Close()
	}

	return svc, cleanup
}

func createTestServiceWithKeys(t *testing.T, port int) (*AuthService, func()) {
	return createTestService(t, testConfig{port: port, withKeys: true})
}

func createTestServiceWithoutKeys(t *testing.T, port int) (*AuthService, func()) {
	return createTestService(t, testConfig{port: port, withKeys: false})
}

// generateTestKeys creates proper PEM-encoded ECDSA keys for testing
func generateTestKeys(t *testing.T) ([]byte, []byte) {
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	assert.NilError(t, err)

	privKeyBytes, err := x509.MarshalECPrivateKey(privKey)
	assert.NilError(t, err)
	privKeyPEM := &pem.Block{Type: "EC PRIVATE KEY", Bytes: privKeyBytes}
	var privBuf bytes.Buffer
	err = pem.Encode(&privBuf, privKeyPEM)
	assert.NilError(t, err)

	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	assert.NilError(t, err)
	pubKeyPEM := &pem.Block{Type: "PUBLIC KEY", Bytes: pubKeyBytes}
	var pubBuf bytes.Buffer
	err = pem.Encode(&pubBuf, pubKeyPEM)
	assert.NilError(t, err)

	return privBuf.Bytes(), pubBuf.Bytes()
}

// Test core functionality flows
func TestRepositoryRegistrationFlow(t *testing.T) {
	svc, cleanup := createTestServiceWithoutKeys(t, 12370)
	defer cleanup()

	t.Run("SuccessfulRegistration", func(t *testing.T) {
		response, err := svc.registerRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       "12345",
		})
		assert.NilError(t, err)
		assert.Assert(t, response != nil)
		assert.Assert(t, response["key"] != nil)
		assert.Equal(t, response["key"], "/repositories/github/12345/key")
	})

	t.Run("InvalidRepositoryID", func(t *testing.T) {
		_, err := svc.registerRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       "invalid-id",
		})
		assert.Assert(t, err != nil, "Expected error for invalid repository ID")
	})

	t.Run("EmptyRepositoryID", func(t *testing.T) {
		_, err := svc.registerRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       "",
		})
		assert.Assert(t, err != nil, "Expected error for empty repository ID")
	})

	t.Run("DuplicateRegistration", func(t *testing.T) {
		response1, err := svc.registerRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       "67890",
		})
		assert.NilError(t, err)

		response2, err := svc.registerRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       "67890",
		})
		assert.NilError(t, err)
		assert.Equal(t, response1["key"], response2["key"])
	})
}

func TestRepositoryUnregistrationFlow(t *testing.T) {
	svc, cleanup := createTestServiceWithoutKeys(t, 12371)
	defer cleanup()

	t.Run("SuccessfulUnregistration", func(t *testing.T) {
		// First register a repository
		_, err := svc.registerRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       "11111",
		})
		assert.NilError(t, err)

		// Then unregister it
		_, err = svc.unregisterRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       "11111",
		})
		assert.NilError(t, err)
	})

	t.Run("UnregisterNonExistentRepository", func(t *testing.T) {
		_, err := svc.unregisterRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       "99999",
		})
		assert.Assert(t, err != nil, "Expected error for non-existent repository")
	})

	t.Run("CompleteRegisterUnregisterFlow", func(t *testing.T) {
		repoID := "22222"

		// Register repository
		_, err := svc.registerRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       repoID,
		})
		assert.NilError(t, err)

		// Unregister repository
		_, err = svc.unregisterRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       repoID,
		})
		assert.NilError(t, err)

		// Verify repository is gone
		_, err = svc.unregisterRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       repoID,
		})
		assert.Assert(t, err != nil, "Expected error for already unregistered repository")
	})
}

func TestDomainRegistrationFlow(t *testing.T) {
	svc, cleanup := createTestServiceWithKeys(t, 12372)
	defer cleanup()

	validProjectID := id.Generate()

	t.Run("SuccessfulDomainRegistration", func(t *testing.T) {
		response, err := svc.ApiDomainServiceHandler(context.Background(), nil, map[string]interface{}{
			"action":  "register",
			"fqdn":    "test.example.com",
			"project": validProjectID,
		})
		assert.NilError(t, err)
		assert.Assert(t, response != nil)
		assert.Assert(t, response["token"] != nil)
		assert.Equal(t, response["type"], "txt")
	})

	t.Run("MissingParameters", func(t *testing.T) {
		testCases := []struct {
			name   string
			params map[string]interface{}
			expect string
		}{
			{"MissingAction", map[string]interface{}{"fqdn": "test.com", "project": validProjectID}, "missing action"},
			{"MissingFQDN", map[string]interface{}{"action": "register", "project": validProjectID}, "missing fqdn"},
			{"MissingProject", map[string]interface{}{"action": "register", "fqdn": "test.com"}, "missing project"},
			{"ShortProjectID", map[string]interface{}{"action": "register", "fqdn": "test.com", "project": "123"}, "short project ID"},
			{"InvalidAction", map[string]interface{}{"action": "invalid", "fqdn": "test.com", "project": validProjectID}, "invalid action"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := svc.ApiDomainServiceHandler(context.Background(), nil, tc.params)
				assert.Assert(t, err != nil, "Expected error for %s", tc.expect)
			})
		}
	})
}

func TestIntegrationFlow(t *testing.T) {
	svc, cleanup := createTestServiceWithKeys(t, 12373)
	defer cleanup()

	validProjectID := id.Generate()

	t.Run("CompleteWorkflow", func(t *testing.T) {
		// Register repository
		repoID := "33333"
		response, err := svc.registerRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       repoID,
		})
		assert.NilError(t, err)
		assert.Equal(t, response["key"], "/repositories/github/33333/key")

		// Register domain
		domainResponse, err := svc.ApiDomainServiceHandler(context.Background(), nil, map[string]interface{}{
			"action":  "register",
			"fqdn":    "integration.example.com",
			"project": validProjectID,
		})
		assert.NilError(t, err)
		assert.Assert(t, domainResponse["token"] != nil)

		// Unregister repository
		_, err = svc.unregisterRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       repoID,
		})
		assert.NilError(t, err)

		// Verify repository is gone
		_, err = svc.unregisterRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       repoID,
		})
		assert.Assert(t, err != nil, "Expected error for already unregistered repository")
	})
}

// Test HTTP and P2P handlers
func TestHTTPStreamHandlers(t *testing.T) {
	svc, cleanup := createTestServiceWithoutKeys(t, 12374)
	defer cleanup()

	t.Run("RepositoryRegistrationStream", func(t *testing.T) {
		response, err := svc.registerRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       "12345",
		})
		assert.NilError(t, err)
		assert.Assert(t, response != nil)
		assert.Assert(t, response["key"] != nil)
	})

	t.Run("RepositoryUnregistrationStream", func(t *testing.T) {
		_, err := svc.registerRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       "67890",
		})
		assert.NilError(t, err)

		response, err := svc.unregisterRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       "67890",
		})
		assert.NilError(t, err)
		assert.Equal(t, response["status"], "success")
	})
}

// Test actual HTTP handlers (the ones used by the HTTP service)
func TestActualHTTPHandlers(t *testing.T) {
	svc, cleanup := createTestServiceWithoutKeys(t, 12375)
	defer cleanup()

	// Mock GitHub client for HTTP handlers
	mockGitHubClient := &mockGitHubClient{}

	t.Run("GitHubRepositoryRegistrationHTTP", func(t *testing.T) {
		// Create mock HTTP context with GitHub client
		mockCtx := &mockHTTPContextWithClient{
			variables: map[string]interface{}{
				"provider":     "github",
				"id":           "11111",
				"GithubClient": mockGitHubClient,
			},
		}

		response, err := svc.registerGitHubUserRepositoryHTTPHandler(mockCtx)
		assert.NilError(t, err)
		assert.Assert(t, response != nil)
	})

	t.Run("GitHubRepositoryUnregistrationHTTP", func(t *testing.T) {
		// First register a repository
		_, err := svc.registerRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       "22222",
		})
		assert.NilError(t, err)

		// Create mock HTTP context with GitHub client
		mockCtx := &mockHTTPContextWithClient{
			variables: map[string]interface{}{
				"provider":     "github",
				"id":           "22222",
				"GithubClient": mockGitHubClient,
			},
		}

		// Test unregistering via HTTP handler
		_, err = svc.unregisterGitHubUserRepositoryHTTPHandler(mockCtx)
		assert.NilError(t, err)
	})

	t.Run("GitHubRepositoryGetHTTP", func(t *testing.T) {
		// First register a repository
		_, err := svc.registerRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       "33333",
		})
		assert.NilError(t, err)

		// Create mock HTTP context
		mockCtx := &mockHTTPContextWithClient{
			variables: map[string]interface{}{
				"provider": "github",
				"id":       "33333",
			},
		}

		// Test getting repository info via HTTP handler
		response, err := svc.getGitHubUserRepositoryHTTPHandler(mockCtx)
		assert.NilError(t, err)
		assert.Assert(t, response != nil)
		assert.Assert(t, response.(map[string]interface{})["hooks"] != nil)
	})

	t.Run("GitHubRepositoryListHTTP", func(t *testing.T) {
		// Create mock HTTP context with GitHub client
		mockCtx := &mockHTTPContextWithClient{
			variables: map[string]interface{}{
				"GithubClient": mockGitHubClient,
			},
		}

		// Test getting repository list via HTTP handler
		response, err := svc.getGitHubUserRepositoriesHTTPHandler(mockCtx)
		assert.NilError(t, err)
		assert.Assert(t, response != nil)
	})
}

// Test P2P handlers (internal service communication)
func TestP2PRepositoryHandlers(t *testing.T) {
	svc, cleanup := createTestServiceWithoutKeys(t, 12378)
	defer cleanup()

	t.Run("RepositoryRegistrationP2P", func(t *testing.T) {
		response, err := svc.registerRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       "12345",
		})
		assert.NilError(t, err)
		assert.Assert(t, response != nil)
		assert.Assert(t, response["key"] != nil)
	})

	t.Run("RepositoryUnregistrationP2P", func(t *testing.T) {
		_, err := svc.registerRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       "67890",
		})
		assert.NilError(t, err)

		_, err = svc.unregisterRepositoryStream(context.Background(), map[string]interface{}{
			"provider": "github",
			"id":       "67890",
		})
		assert.NilError(t, err)
	})

	t.Run("ErrorCases", func(t *testing.T) {
		testCases := []struct {
			name   string
			params map[string]interface{}
			expect string
		}{
			{"UnsupportedProvider", map[string]interface{}{"provider": "bitbucket", "id": "12345"}, "unsupported provider"},
			{"MissingProvider", map[string]interface{}{"id": "12345"}, "missing provider"},
			{"MissingID", map[string]interface{}{"provider": "github"}, "missing ID"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := svc.registerRepositoryStream(context.Background(), tc.params)
				assert.Assert(t, err != nil, "Expected error for %s", tc.expect)
			})
		}
	})
}

// Test error handling and edge cases
func TestErrorHandling(t *testing.T) {
	svc, cleanup := createTestServiceWithoutKeys(t, 12379)
	defer cleanup()

	t.Run("RepositoryErrors", func(t *testing.T) {
		testCases := []struct {
			name string
			id   string
		}{
			{"InvalidID", "invalid-id"},
			{"EmptyID", ""},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := svc.registerRepositoryStream(context.Background(), map[string]interface{}{"provider": "github", "id": tc.id})
				assert.Assert(t, err != nil, "Expected error for %s", tc.name)
			})
		}
	})

	t.Run("UnregistrationErrors", func(t *testing.T) {
		testCases := []struct {
			name string
			id   string
		}{
			{"NonExistent", "99999"},
			{"InvalidID", "invalid-id"},
			{"EmptyID", ""},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := svc.unregisterRepositoryStream(context.Background(), map[string]interface{}{"provider": "github", "id": tc.id})
				assert.Assert(t, err != nil, "Expected error for %s", tc.name)
			})
		}
	})
}

// Test service operations and stream error handling
func TestServiceOperations(t *testing.T) {
	svc, cleanup := createTestServiceWithKeys(t, 12380)
	defer cleanup()

	t.Run("ServiceMethods", func(t *testing.T) {
		assert.Assert(t, svc.Node() != nil)
		assert.Assert(t, svc.KV() != nil)
		assert.Assert(t, svc.stream != nil)
		assert.Assert(t, svc.http != nil)
	})

	t.Run("StreamErrorHandling", func(t *testing.T) {
		// Test repository stream errors
		_, err := svc.registerRepositoryStream(context.Background(), map[string]interface{}{"provider": "github"})
		assert.Assert(t, err != nil, "Expected error for missing ID")

		_, err = svc.unregisterRepositoryStream(context.Background(), map[string]interface{}{"provider": "github"})
		assert.Assert(t, err != nil, "Expected error for missing ID")

		// Test domain stream errors
		_, err = svc.registerDomainStream(context.Background(), map[string]interface{}{"action": "register", "project": id.Generate()})
		assert.Assert(t, err != nil, "Expected error for missing fqdn")

		_, err = svc.registerDomainStream(context.Background(), map[string]interface{}{"action": "register", "fqdn": "test.com"})
		assert.Assert(t, err != nil, "Expected error for missing project")
	})
}

// Test comprehensive repository operations
func TestRepositoryOperations(t *testing.T) {
	svc, cleanup := createTestServiceWithKeys(t, 12381)
	defer cleanup()

	t.Run("MultipleRepositoryOperations", func(t *testing.T) {
		repoIDs := []string{"111111", "222222", "333333", "444444", "555555"}

		for _, repoID := range repoIDs {
			_, err := svc.registerRepositoryStream(context.Background(), map[string]interface{}{"provider": "github", "id": repoID})
			assert.NilError(t, err)
		}

		for _, repoID := range repoIDs {
			_, err := svc.unregisterRepositoryStream(context.Background(), map[string]interface{}{"provider": "github", "id": repoID})
			assert.NilError(t, err)
		}
	})

	t.Run("MultipleDomainRegistrations", func(t *testing.T) {
		domains := []string{"test1.example.com", "test2.example.com", "test3.example.com", "test4.example.com", "test5.example.com"}

		for _, domain := range domains {
			response, err := svc.ApiDomainServiceHandler(context.Background(), nil, map[string]interface{}{
				"action":  "register",
				"fqdn":    domain,
				"project": id.Generate(),
			})
			assert.NilError(t, err)
			assert.Assert(t, response != nil)
			assert.Assert(t, response["token"] != nil)
		}
	})

	t.Run("StreamHandlerScenarios", func(t *testing.T) {
		testCases := []map[string]interface{}{
			{"provider": "github", "id": "666666"},
			{"provider": "github", "id": "777777"},
			{"provider": "github", "id": "888888"},
		}

		for _, testCase := range testCases {
			response, err := svc.registerRepositoryStream(context.Background(), testCase)
			assert.NilError(t, err)
			assert.Assert(t, response != nil)

			_, err = svc.unregisterRepositoryStream(context.Background(), testCase)
			assert.NilError(t, err)
		}
	})

	t.Run("InvalidInputHandling", func(t *testing.T) {
		invalidInputs := []map[string]interface{}{
			{},
			nil,
			{"provider": "github"},
			{"id": "12345"},
			{"provider": "bitbucket", "id": "12345"},
		}

		for _, invalidInput := range invalidInputs {
			_, err := svc.registerRepositoryStream(context.Background(), invalidInput)
			assert.Assert(t, err != nil, "Expected error for invalid input: %v", invalidInput)
		}
	})
}

// mockHTTPContextWithClient is a mock that can handle the GitHub client
type mockHTTPContextWithClient struct {
	variables map[string]interface{}
	body      []byte
}

func (m *mockHTTPContextWithClient) GetStringVariable(key string) (string, error) {
	if val, exists := m.variables[key]; exists {
		if strVal, ok := val.(string); ok {
			return strVal, nil
		}
	}
	return "", fmt.Errorf("variable %s not found or not a string", key)
}

func (m *mockHTTPContextWithClient) GetVariable(key string) (interface{}, error) {
	if val, exists := m.variables[key]; exists {
		return val, nil
	}
	return nil, fmt.Errorf("variable %s not found", key)
}

func (m *mockHTTPContextWithClient) Body() []byte {
	return m.body
}

func (m *mockHTTPContextWithClient) SetBody(body []byte) {
	m.body = body
}

func (m *mockHTTPContextWithClient) Variables() map[string]interface{} {
	return m.variables
}

func (m *mockHTTPContextWithClient) SetVariable(key string, val interface{}) {
	m.variables[key] = val
}

func (m *mockHTTPContextWithClient) Request() *http.Request {
	req, _ := http.NewRequest("GET", "/test", nil)
	return req
}

func (m *mockHTTPContextWithClient) Writer() http.ResponseWriter {
	return nil
}

func (m *mockHTTPContextWithClient) ParseBody(obj interface{}) error {
	return nil
}

func (m *mockHTTPContextWithClient) RawResponse() bool {
	return false
}

func (m *mockHTTPContextWithClient) SetRawResponse(val bool) {
}

func (m *mockHTTPContextWithClient) HandleWith(handler httpPkg.Handler) error {
	return nil
}

func (m *mockHTTPContextWithClient) HandleAuth(handler httpPkg.Handler) error {
	return nil
}

func (m *mockHTTPContextWithClient) HandleCleanup(handler httpPkg.Handler) error {
	return nil
}

func (m *mockHTTPContextWithClient) GetStringArrayVariable(key string) ([]string, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockHTTPContextWithClient) GetStringMapVariable(key string) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockHTTPContextWithClient) GetIntVariable(key string) (int, error) {
	return 0, fmt.Errorf("not implemented")
}
