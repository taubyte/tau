package websocket

import (
	"context"
	"testing"
	"time"

	pubsubMsg "github.com/libp2p/go-libp2p-pubsub"
	"github.com/taubyte/tau/core/services/substrate/components"
	"github.com/taubyte/tau/core/services/substrate/components/pubsub"
	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

// Mock serviceable for testing
type mockServiceable struct{}

func (m *mockServiceable) HandleMessage(msg *pubsubMsg.Message) (time.Time, error) {
	return time.Now(), nil
}

func (m *mockServiceable) Name() string {
	return "mock-serviceable"
}

func (m *mockServiceable) Project() string {
	return "test-project"
}

func (m *mockServiceable) Application() string {
	return "test-app"
}

func (m *mockServiceable) Config() *structureSpec.Function {
	return &structureSpec.Function{}
}

func (m *mockServiceable) Match(def components.MatchDefinition) matcherSpec.Index {
	return 0
}

func (m *mockServiceable) Validate(def components.MatchDefinition) error {
	return nil
}

func (m *mockServiceable) Matcher() components.MatchDefinition {
	return &mockMatchDefinition{}
}

func (m *mockServiceable) Ready() error {
	return nil
}

func (m *mockServiceable) Id() string {
	return "mock-id"
}

func (m *mockServiceable) Commit() string {
	return "mock-commit"
}

func (m *mockServiceable) Branch() string {
	return "mock-branch"
}

func (m *mockServiceable) AssetId() string {
	return "mock-asset-id"
}

func (m *mockServiceable) Service() components.ServiceComponent {
	return &mockLocalService{}
}

func (m *mockServiceable) Close() {
	// Mock close
}

func TestDataStreamHandler_SmartOps(t *testing.T) {
	t.Run("empty picks", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create mock service
		mockSrv := &mockLocalService{
			contextFunc: func() context.Context {
				return ctx
			},
		}

		handler := &dataStreamHandler{
			ctx:   ctx,
			ctxC:  cancel,
			srv:   mockSrv,
			picks: []pubsub.Serviceable{}, // empty picks
		}

		err := handler.SmartOps()

		// Should succeed with empty picks
		if err != nil {
			t.Errorf("Expected no error with empty picks, got %v", err)
		}
	})

	t.Run("nil picks", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create mock service
		mockSrv := &mockLocalService{
			contextFunc: func() context.Context {
				return ctx
			},
		}

		handler := &dataStreamHandler{
			ctx:   ctx,
			ctxC:  cancel,
			srv:   mockSrv,
			picks: nil, // nil picks
		}

		err := handler.SmartOps()

		// Should succeed with nil picks
		if err != nil {
			t.Errorf("Expected no error with nil picks, got %v", err)
		}
	})

	t.Run("non-websocket pick", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create a mock service that's not a WebSocket
		mockService := &mockServiceable{}

		// Create mock service
		mockSrv := &mockLocalService{
			contextFunc: func() context.Context {
				return ctx
			},
		}

		handler := &dataStreamHandler{
			ctx:   ctx,
			ctxC:  cancel,
			srv:   mockSrv,
			picks: []pubsub.Serviceable{mockService}, // non-WebSocket pick
		}

		err := handler.SmartOps()

		// Should return error for non-WebSocket pick
		if err == nil {
			t.Error("Expected error for non-WebSocket pick, but got nil")
		}

		expectedErr := "tried to run a smartOp on a websocket that was not a websocket"
		if err.Error() != expectedErr {
			t.Errorf("Expected error '%s', got '%s'", expectedErr, err.Error())
		}
	})
}
