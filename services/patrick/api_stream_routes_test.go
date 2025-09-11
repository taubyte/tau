package service

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/taubyte/tau/p2p/streams/command/router"
)

// mockStreamService is a simple mock for testing stream route registration
type mockStreamService struct {
	mock.Mock
}

func (m *mockStreamService) Define(command string, handler router.CommandHandler) error {
	args := m.Called(command, handler)
	return args.Error(0)
}

func (m *mockStreamService) DefineStream(command string, std router.CommandHandler, stream router.StreamHandler) error {
	args := m.Called(command, std, stream)
	return args.Error(0)
}

func (m *mockStreamService) Stop() {
	m.Called()
}

func (m *mockStreamService) Router() *router.Router {
	args := m.Called()
	return args.Get(0).(*router.Router)
}

func TestSetupStreamRoutes(t *testing.T) {
	mockStream := &mockStreamService{}

	// Expect the stream routes to be defined
	mockStream.On("Define", "ping", mock.AnythingOfType("router.CommandHandler")).Return(nil)
	mockStream.On("Define", "patrick", mock.AnythingOfType("router.CommandHandler")).Return(nil)
	mockStream.On("Define", "stats", mock.AnythingOfType("router.CommandHandler")).Return(nil)

	// Create a test service with the mock stream
	srv := &PatrickService{}
	srv.stream = mockStream

	// Call the method
	srv.setupStreamRoutes()

	// Verify
	mockStream.AssertExpectations(t)
}
