package service

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/mock"
	httpPkg "github.com/taubyte/tau/pkg/http"
)

// mockHTTPService is a simple mock for testing route registration
type mockHTTPService struct {
	mock.Mock
}

func (m *mockHTTPService) Context() context.Context                            { return context.Background() }
func (m *mockHTTPService) Start()                                              {}
func (m *mockHTTPService) Stop()                                               {}
func (m *mockHTTPService) Wait() error                                         { return nil }
func (m *mockHTTPService) Error() error                                        { return nil }
func (m *mockHTTPService) GET(def *httpPkg.RouteDefinition)                    { m.Called(def.Path) }
func (m *mockHTTPService) PUT(def *httpPkg.RouteDefinition)                    { m.Called(def.Path) }
func (m *mockHTTPService) POST(def *httpPkg.RouteDefinition)                   { m.Called(def.Path) }
func (m *mockHTTPService) DELETE(def *httpPkg.RouteDefinition)                 { m.Called(def.Path) }
func (m *mockHTTPService) PATCH(def *httpPkg.RouteDefinition)                  { m.Called(def.Path) }
func (m *mockHTTPService) ALL(def *httpPkg.RouteDefinition)                    { m.Called(def.Path) }
func (m *mockHTTPService) Raw(def *httpPkg.RawRouteDefinition) *mux.Route      { return nil }
func (m *mockHTTPService) LowLevel(def *httpPkg.LowLevelDefinition) *mux.Route { return nil }
func (m *mockHTTPService) WebSocket(def *httpPkg.WebSocketDefinition)          {}
func (m *mockHTTPService) ServeAssets(def *httpPkg.AssetsDefinition)           {}
func (m *mockHTTPService) AssetHandler(def *httpPkg.HeadlessAssetsDefinition, ctx httpPkg.Context) (interface{}, error) {
	return nil, nil
}
func (m *mockHTTPService) LowLevelAssetHandler(def *httpPkg.HeadlessAssetsDefinition, w http.ResponseWriter, r *http.Request) error {
	return nil
}
func (m *mockHTTPService) GetListenAddress() (*url.URL, error) { return nil, nil }

func TestSetupGithubRoutes(t *testing.T) {
	mockHTTP := &mockHTTPService{}

	// Expect the routes to be registered
	mockHTTP.On("POST", "/github/{hook}").Return()
	mockHTTP.On("GET", "/ping").Return()

	// Create a simple test service
	srv := &PatrickService{
		http:    mockHTTP,
		devMode: true,
		hostUrl: "",
	}

	// Call the method
	srv.setupGithubRoutes()

	// Verify
	mockHTTP.AssertExpectations(t)
}

func TestSetupGithubRoutesInProduction(t *testing.T) {
	mockHTTP := &mockHTTPService{}

	// Expect the routes to be registered with production host
	mockHTTP.On("POST", "/github/{hook}").Return()
	mockHTTP.On("GET", "/ping").Return()

	// Create a test service in production mode
	srv := &PatrickService{
		http:    mockHTTP,
		devMode: false,
		hostUrl: "example.com",
	}

	// Call the method
	srv.setupGithubRoutes()

	// Verify
	mockHTTP.AssertExpectations(t)
}

func TestSetupJobRoutes(t *testing.T) {
	mockHTTP := &mockHTTPService{}

	// Expect all job routes
	mockHTTP.On("GET", "/jobs/{projectId}").Return()
	mockHTTP.On("GET", "/job/{jid}").Return()
	mockHTTP.On("GET", "/download/{jobId}/{resourceId}").Return()
	mockHTTP.On("GET", "/logs/{cid}").Return()
	mockHTTP.On("POST", "/cancel/{jid}").Return()
	mockHTTP.On("POST", "/retry/{jid}").Return()

	// Create a simple test service
	srv := &PatrickService{
		http:    mockHTTP,
		devMode: true,
		hostUrl: "",
	}

	// Call the method
	srv.setupJobRoutes()

	// Verify
	mockHTTP.AssertExpectations(t)
}

func TestSetupJobRoutesInProduction(t *testing.T) {
	mockHTTP := &mockHTTPService{}

	// Expect all job routes with production host
	mockHTTP.On("GET", "/jobs/{projectId}").Return()
	mockHTTP.On("GET", "/job/{jid}").Return()
	mockHTTP.On("GET", "/download/{jobId}/{resourceId}").Return()
	mockHTTP.On("GET", "/logs/{cid}").Return()
	mockHTTP.On("POST", "/cancel/{jid}").Return()
	mockHTTP.On("POST", "/retry/{jid}").Return()

	// Create a test service in production mode
	srv := &PatrickService{
		http:    mockHTTP,
		devMode: false,
		hostUrl: "example.com",
	}

	// Call the method
	srv.setupJobRoutes()

	// Verify
	mockHTTP.AssertExpectations(t)
}
