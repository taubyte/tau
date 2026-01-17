package auth

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb/mock"
)

// TestConfig holds common test configuration
type TestConfig struct {
	Port        int
	NetworkFqdn string
	DevMode     bool
	UseMockNode bool
	UseMockDB   bool
	TempDir     string
	CustomKeys  bool
}

// DefaultTestConfig returns a default test configuration
func DefaultTestConfig() *TestConfig {
	return &TestConfig{
		Port:        12351,
		NetworkFqdn: "test.tau",
		DevMode:     false,
		UseMockNode: false,
		UseMockDB:   true,
		TempDir:     "",
		CustomKeys:  false,
	}
}

// CreateTestConfig creates a config.Node for testing based on TestConfig
func CreateTestConfig(t *testing.T, cfg *TestConfig) *config.Node {
	if cfg == nil {
		cfg = DefaultTestConfig()
	}

	if cfg.TempDir == "" {
		cfg.TempDir = t.TempDir()
	}

	nodeConfig := &config.Node{
		NetworkFqdn: cfg.NetworkFqdn,
		DevMode:     cfg.DevMode,
		P2PListen:   []string{fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", cfg.Port)},
		P2PAnnounce: []string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", cfg.Port)},
		PrivateKey:  keypair.NewRaw(),
		Root:        cfg.TempDir,
	}

	if cfg.UseMockDB {
		nodeConfig.Databases = mock.New()
	}

	if cfg.UseMockNode {
		ctx := context.Background()
		mockNode := peer.Mock(ctx)
		nodeConfig.Node = mockNode

		if cfg.CustomKeys {
			nodeConfig.DomainValidation = config.DomainValidation{
				PrivateKey: []byte("private-key"),
				PublicKey:  []byte("public-key"),
			}
		}
	}

	return nodeConfig
}

// CreateTestService creates an auth service for testing with the given config
func CreateTestService(t *testing.T, cfg *TestConfig) (*AuthService, func()) {
	ctx := context.Background()
	nodeConfig := CreateTestConfig(t, cfg)

	svc, err := New(ctx, nodeConfig)
	if err != nil {
		t.Fatalf("Failed to create test service: %v", err)
	}

	cleanup := func() {
		if svc != nil {
			svc.Close()
		}
	}

	return svc, cleanup
}

// CreateTestServiceWithPort creates an auth service with a specific port
func CreateTestServiceWithPort(t *testing.T, port int) (*AuthService, func()) {
	cfg := DefaultTestConfig()
	cfg.Port = port
	return CreateTestService(t, cfg)
}

// CreateTestServiceWithMockNode creates an auth service with a mock node
func CreateTestServiceWithMockNode(t *testing.T, port int) (*AuthService, func()) {
	cfg := DefaultTestConfig()
	cfg.Port = port
	cfg.UseMockNode = true
	cfg.CustomKeys = true
	return CreateTestService(t, cfg)
}

// CreateTestServiceWithCustomConfig creates an auth service with custom configuration
func CreateTestServiceWithCustomConfig(t *testing.T, networkFqdn string, devMode bool, port int) (*AuthService, func()) {
	cfg := DefaultTestConfig()
	cfg.NetworkFqdn = networkFqdn
	cfg.DevMode = devMode
	cfg.Port = port
	return CreateTestService(t, cfg)
}

// MockHTTPContext implements the http.Context interface for testing
type MockHTTPContext struct {
	request   *http.Request
	writer    http.ResponseWriter
	variables map[string]interface{}
	body      []byte
}

// NewMockHTTPContext creates a new MockHTTPContext for testing
func NewMockHTTPContext() *MockHTTPContext {
	return &MockHTTPContext{
		variables: make(map[string]interface{}),
	}
}

// SetRequest sets the HTTP request for the mock context
func (m *MockHTTPContext) SetRequest(req *http.Request) {
	m.request = req
}

// SetWriter sets the HTTP response writer for the mock context
func (m *MockHTTPContext) SetWriter(w http.ResponseWriter) {
	m.writer = w
}

// SetVariables sets multiple variables at once
func (m *MockHTTPContext) SetVariables(vars map[string]interface{}) {
	for k, v := range vars {
		m.variables[k] = v
	}
}

// SetBody sets the request body for the mock context
func (m *MockHTTPContext) SetBody(body []byte) {
	m.body = body
}

// HTTPContext interface methods
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

// TestCase represents a test case with setup and teardown
type TestCase struct {
	Name     string
	Setup    func(t *testing.T) (*AuthService, func())
	Test     func(t *testing.T, svc *AuthService)
	Teardown func(t *testing.T, svc *AuthService)
}

// RunTestCases runs multiple test cases with proper setup and teardown
func RunTestCases(t *testing.T, testCases []TestCase) {
	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			var svc *AuthService
			var cleanup func()

			if tc.Setup != nil {
				svc, cleanup = tc.Setup(t)
				defer cleanup()
			}

			if tc.Test != nil {
				tc.Test(t, svc)
			}

			if tc.Teardown != nil {
				tc.Teardown(t, svc)
			}
		})
	}
}

// Common test assertions and utilities
func AssertServiceCreated(t *testing.T, svc *AuthService, err error) {
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	if svc == nil {
		t.Fatal("Service should not be nil")
	}
}

func AssertServiceClosed(t *testing.T, svc *AuthService, err error) {
	if err != nil {
		t.Fatalf("Failed to close service: %v", err)
	}
}

// PortGenerator generates unique ports for tests to avoid conflicts
type PortGenerator struct {
	startPort int
	current   int
}

// NewPortGenerator creates a new port generator starting from the given port
func NewPortGenerator(startPort int) *PortGenerator {
	return &PortGenerator{
		startPort: startPort,
		current:   startPort,
	}
}

// NextPort returns the next available port
func (pg *PortGenerator) NextPort() int {
	port := pg.current
	pg.current++
	return port
}

// Reset resets the port generator to the start port
func (pg *PortGenerator) Reset() {
	pg.current = pg.startPort
}
