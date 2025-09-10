package websocket

import (
	"context"
	"errors"
	"net/http"
	"testing"

	pubsubMsg "github.com/libp2p/go-libp2p-pubsub"
	iface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/core/services/tns"
	p2p "github.com/taubyte/tau/p2p/peer"
	service "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
	"gotest.tools/v3/assert"
)

// Mock for service.Context
type mockServiceContext struct {
	getStringVariableFunc func(key string) (string, error)
}

func (m *mockServiceContext) HandleWith(handler service.Handler) error    { return nil }
func (m *mockServiceContext) HandleAuth(handler service.Handler) error    { return nil }
func (m *mockServiceContext) HandleCleanup(handler service.Handler) error { return nil }
func (m *mockServiceContext) Request() *http.Request                      { return nil }
func (m *mockServiceContext) Writer() http.ResponseWriter                 { return nil }
func (m *mockServiceContext) ParseBody(obj interface{}) error             { return nil }
func (m *mockServiceContext) RawResponse() bool                           { return false }
func (m *mockServiceContext) SetRawResponse(val bool)                     {}
func (m *mockServiceContext) Variables() map[string]interface{}           { return nil }
func (m *mockServiceContext) SetVariable(key string, val interface{})     {}
func (m *mockServiceContext) Body() []byte                                { return nil }
func (m *mockServiceContext) SetBody([]byte)                              {}
func (m *mockServiceContext) GetStringVariable(key string) (string, error) {
	if m.getStringVariableFunc != nil {
		return m.getStringVariableFunc(key)
	}
	return "", errors.New("not implemented")
}
func (m *mockServiceContext) GetStringArrayVariable(key string) ([]string, error) { return nil, nil }
func (m *mockServiceContext) GetStringMapVariable(key string) (map[string]interface{}, error) {
	return nil, nil
}
func (m *mockServiceContext) GetIntVariable(key string) (int, error) { return 0, nil }

// Create a separate mock for Handler tests that includes Tns and Lookup methods
type mockLocalServiceForHandler struct {
	common.LocalService
}

func (m *mockLocalServiceForHandler) Tns() tns.Client {
	return &mockTnsClient{}
}

func (m *mockLocalServiceForHandler) Lookup(matcher *common.MatchDefinition) ([]iface.Serviceable, error) {
	return nil, errors.New("lookup failed")
}

// Simple mock TNS client - only implement what's actually called
type mockTnsClient struct {
	tns.Client
}

func (m *mockTnsClient) Fetch(path tns.Path) (tns.Object, error) {
	return nil, errors.New("TNS fetch failed")
}

func TestSubViewer_getNextId(t *testing.T) {
	t.Run("increments correctly", func(t *testing.T) {
		sv := &subViewer{
			subs:   make(map[int]*sub),
			nextId: 0,
		}

		// Test multiple calls to getNextId
		for i := 0; i < 5; i++ {
			id := sv.getNextId()
			assert.Equal(t, id, i)
		}

		// Verify nextId was incremented
		assert.Equal(t, sv.nextId, 5)
	})
}

func TestSubViewer_handler(t *testing.T) {
	t.Run("calls all subscription handlers", func(t *testing.T) {
		sv := &subViewer{
			subs:   make(map[int]*sub),
			nextId: 0,
		}

		// Add some mock subscriptions
		handler1Called := false
		handler2Called := false

		sv.subs[1] = &sub{
			handler: func(msg *pubsubMsg.Message) {
				handler1Called = true
			},
		}

		sv.subs[2] = &sub{
			handler: func(msg *pubsubMsg.Message) {
				handler2Called = true
			},
		}

		// Call handler
		sv.handler(&pubsubMsg.Message{})

		assert.Assert(t, handler1Called, "Expected handler1 to be called")
		assert.Assert(t, handler2Called, "Expected handler2 to be called")
	})

	t.Run("handles empty subscriptions", func(t *testing.T) {
		sv := &subViewer{
			subs:   make(map[int]*sub),
			nextId: 0,
		}

		// Call handler with no subscriptions
		sv.handler(&pubsubMsg.Message{})

		// Should not panic
	})
}

func TestSubViewer_err_handler(t *testing.T) {
	t.Run("calls all error handlers", func(t *testing.T) {
		sv := &subViewer{
			subs:   make(map[int]*sub),
			nextId: 0,
		}

		// Add some mock subscriptions
		errHandler1Called := false
		errHandler2Called := false

		sv.subs[1] = &sub{
			err_handler: func(err error) {
				errHandler1Called = true
			},
		}

		sv.subs[2] = &sub{
			err_handler: func(err error) {
				errHandler2Called = true
			},
		}

		// Call err_handler
		sv.err_handler(nil)

		assert.Assert(t, errHandler1Called, "Expected errHandler1 to be called")
		assert.Assert(t, errHandler2Called, "Expected errHandler2 to be called")
	})

	t.Run("handles empty subscriptions", func(t *testing.T) {
		sv := &subViewer{
			subs:   make(map[int]*sub),
			nextId: 0,
		}

		// Call err_handler with no subscriptions
		sv.err_handler(nil)

		// Should not panic
	})
}

func TestRemoveSubscription(t *testing.T) {
	t.Run("removes existing subscription", func(t *testing.T) {
		// Reset global subs for testing
		subs = &subsViewer{
			subscriptions: make(map[string]*subViewer),
		}

		// Create a subscription
		sv := &subViewer{
			subs:   make(map[int]*sub),
			nextId: 0,
		}
		sv.subs[1] = &sub{}
		subs.subscriptions["test-topic"] = sv

		// Remove subscription
		removeSubscription("test-topic", 1)

		// Verify subscription was removed
		_, exists := sv.subs[1]
		assert.Assert(t, !exists, "Expected subscription to be removed")
	})

	t.Run("handles non-existent topic", func(t *testing.T) {
		// Reset global subs for testing
		subs = &subsViewer{
			subscriptions: make(map[string]*subViewer),
		}

		// Try to remove from non-existent topic
		removeSubscription("non-existent-topic", 1)

		// Should not panic
	})

	t.Run("handles non-existent subscription", func(t *testing.T) {
		// Reset global subs for testing
		subs = &subsViewer{
			subscriptions: make(map[string]*subViewer),
		}

		// Create a subscription
		sv := &subViewer{
			subs:   make(map[int]*sub),
			nextId: 0,
		}
		subs.subscriptions["test-topic"] = sv

		// Try to remove non-existent subscription
		removeSubscription("test-topic", 999)

		// Should not panic
	})
}

func TestAddSubscription(t *testing.T) {
	t.Run("creates new subscription", func(t *testing.T) {
		// Reset global subs for testing
		subs = &subsViewer{
			subscriptions: make(map[string]*subViewer),
		}

		// Create mock service
		mockSrv := &mockLocalService{
			contextFunc: func() context.Context {
				return context.Background()
			},
			pubSubSubscribeFunc: func(topic string, handler p2p.PubSubConsumerHandler, errHandler p2p.PubSubConsumerErrorHandler) error {
				return nil
			},
		}

		// Add subscription
		id, err := AddSubscription(mockSrv, "test-topic", func(msg *pubsubMsg.Message) {}, func(err error) {})

		assert.NilError(t, err)
		assert.Equal(t, id, 0)

		// Verify subscription was added
		subset, exists := subs.subscriptions["test-topic"]
		assert.Assert(t, exists, "Expected subscription to be created")
		assert.Equal(t, len(subset.subs), 1)
	})

	t.Run("adds to existing subscription", func(t *testing.T) {
		// Reset global subs for testing
		subs = &subsViewer{
			subscriptions: make(map[string]*subViewer),
		}

		// Create existing subscription
		sv := &subViewer{
			subs:   make(map[int]*sub),
			nextId: 1, // Start from 1 since we already have one subscription
		}
		sv.subs[0] = &sub{}
		subs.subscriptions["test-topic"] = sv

		// Create mock service
		mockSrv := &mockLocalService{
			contextFunc: func() context.Context {
				return context.Background()
			},
		}

		// Add another subscription
		id, err := AddSubscription(mockSrv, "test-topic", func(msg *pubsubMsg.Message) {}, func(err error) {})

		assert.NilError(t, err)
		assert.Equal(t, id, 1)

		// Verify subscription was added
		assert.Equal(t, len(sv.subs), 2)
	})

	t.Run("handles pubsub subscribe error", func(t *testing.T) {
		// Reset global subs for testing
		subs = &subsViewer{
			subscriptions: make(map[string]*subViewer),
		}

		// Create mock service that returns error
		mockSrv := &mockLocalService{
			contextFunc: func() context.Context {
				return context.Background()
			},
			pubSubSubscribeFunc: func(topic string, handler p2p.PubSubConsumerHandler, errHandler p2p.PubSubConsumerErrorHandler) error {
				return errors.New("pubsub error")
			},
		}

		// Add subscription
		_, err := AddSubscription(mockSrv, "test-topic", func(msg *pubsubMsg.Message) {}, func(err error) {})

		assert.Error(t, err, "pubsub subscribe failed with: pubsub error")
	})
}

func TestHandler(t *testing.T) {
	t.Run("handles createWsHandler error", func(t *testing.T) {
		// Create mock context that returns error for hash
		ctx := &mockServiceContext{
			getStringVariableFunc: func(key string) (string, error) {
				return "", errors.New("hash not found")
			},
		}

		// Create mock connection
		conn := &mockWebSocketConnection{
			writeJSONFunc: func(v interface{}) error {
				return nil
			},
			closeFunc: func() error {
				return nil
			},
		}

		// Create mock service
		mockSrv := &mockLocalServiceForHandler{}

		// Call Handler function
		result := Handler(mockSrv, ctx, conn)

		// Should return nil due to error
		assert.Assert(t, result == nil, "Expected handler to be nil due to error")
	})
}

func TestHandler_CreateWsHandlerErrorPaths(t *testing.T) {
	t.Run("missing hash variable", func(t *testing.T) {
		ctx := &mockServiceContext{
			getStringVariableFunc: func(key string) (string, error) {
				if key == "hash" {
					return "", errors.New("hash not found")
				}
				return "", errors.New("unknown key")
			},
		}

		conn := &mockWebSocketConnection{
			writeJSONFunc: func(v interface{}) error { return nil },
			closeFunc:     func() error { return nil },
		}
		mockSrv := &mockLocalServiceForHandler{}

		result := Handler(mockSrv, ctx, conn)
		assert.Assert(t, result == nil, "Expected handler to be nil due to hash error")
	})

	t.Run("missing channel variable", func(t *testing.T) {
		ctx := &mockServiceContext{
			getStringVariableFunc: func(key string) (string, error) {
				if key == "hash" {
					return "test-hash", nil
				}
				if key == "channel" {
					return "", errors.New("channel not found")
				}
				return "", errors.New("unknown key")
			},
		}

		conn := &mockWebSocketConnection{
			writeJSONFunc: func(v interface{}) error { return nil },
			closeFunc:     func() error { return nil },
		}
		mockSrv := &mockLocalServiceForHandler{}

		result := Handler(mockSrv, ctx, conn)
		assert.Assert(t, result == nil, "Expected handler to be nil due to channel error")
	})

	t.Run("messagingSpec.Tns().WebSocketPath error", func(t *testing.T) {
		ctx := &mockServiceContext{
			getStringVariableFunc: func(key string) (string, error) {
				if key == "hash" {
					return "", nil // Empty hash will cause messagingSpec.Tns().WebSocketPath() to fail
				}
				if key == "channel" {
					return "test-channel", nil
				}
				return "", errors.New("unknown key")
			},
		}

		conn := &mockWebSocketConnection{
			writeJSONFunc: func(v interface{}) error { return nil },
			closeFunc:     func() error { return nil },
		}
		mockSrv := &mockLocalServiceForHandler{}

		result := Handler(mockSrv, ctx, conn)
		assert.Assert(t, result == nil, "Expected handler to be nil due to WebSocketPath error")
	})

	t.Run("TNS fetch error", func(t *testing.T) {
		ctx := &mockServiceContext{
			getStringVariableFunc: func(key string) (string, error) {
				if key == "hash" {
					return "test-hash", nil
				}
				if key == "channel" {
					return "test-channel", nil
				}
				return "", errors.New("unknown key")
			},
		}

		conn := &mockWebSocketConnection{
			writeJSONFunc: func(v interface{}) error { return nil },
			closeFunc:     func() error { return nil },
		}
		mockSrv := &mockLocalServiceForHandler{}

		result := Handler(mockSrv, ctx, conn)
		assert.Assert(t, result == nil, "Expected handler to be nil due to TNS fetch error")
	})

	t.Run("AddSubscription error", func(t *testing.T) {
		// This test would require mocking the success path of createWsHandler
		// but then failing AddSubscription. This is complex due to dependencies.
		t.Skip("AddSubscription error test requires complex success path mocking")
	})

	t.Run("Handler success path", func(t *testing.T) {
		// This test would require mocking the entire success path through createWsHandler
		// and AddSubscription. This is complex due to TNS and messaging dependencies.
		t.Skip("Handler success path test requires complex end-to-end mocking")
	})
}

func TestCreateWsHandler_AdditionalErrorPaths(t *testing.T) {
	// messagingSpec.Tns().WebSocketPath error is already tested above with empty hash

	t.Run("lookup error", func(t *testing.T) {
		ctx := &mockServiceContext{
			getStringVariableFunc: func(key string) (string, error) {
				if key == "hash" {
					return "valid-hash", nil
				}
				if key == "channel" {
					return "test-channel", nil
				}
				return "", errors.New("unknown key")
			},
		}

		conn := &mockWebSocketConnection{
			writeJSONFunc: func(v interface{}) error { return nil },
			closeFunc:     func() error { return nil },
		}

		// Create a mock service that succeeds up to lookup then fails
		mockSrv := &mockLocalServiceForHandlerWithLookupError{}

		result := Handler(mockSrv, ctx, conn)
		assert.Assert(t, result == nil, "Expected handler to be nil due to lookup error")
	})

	t.Run("empty picks from lookup", func(t *testing.T) {
		ctx := &mockServiceContext{
			getStringVariableFunc: func(key string) (string, error) {
				if key == "hash" {
					return "valid-hash", nil
				}
				if key == "channel" {
					return "test-channel", nil
				}
				return "", errors.New("unknown key")
			},
		}

		conn := &mockWebSocketConnection{
			writeJSONFunc: func(v interface{}) error { return nil },
			closeFunc:     func() error { return nil },
		}

		// Create a mock service that succeeds up to lookup then returns empty picks
		mockSrv := &mockLocalServiceForHandlerWithEmptyPicks{}

		result := Handler(mockSrv, ctx, conn)
		assert.Assert(t, result == nil, "Expected handler to be nil due to empty picks")
	})
}

// Simple mock services for the remaining error paths
type mockLocalServiceForHandlerWithLookupError struct {
	common.LocalService
}

func (m *mockLocalServiceForHandlerWithLookupError) Tns() tns.Client {
	return &mockTnsClientWithValidPath{}
}

func (m *mockLocalServiceForHandlerWithLookupError) Lookup(matcher *common.MatchDefinition) ([]iface.Serviceable, error) {
	return nil, errors.New("lookup failed")
}

type mockLocalServiceForHandlerWithEmptyPicks struct {
	common.LocalService
}

func (m *mockLocalServiceForHandlerWithEmptyPicks) Tns() tns.Client {
	return &mockTnsClientWithValidPath{}
}

func (m *mockLocalServiceForHandlerWithEmptyPicks) Lookup(matcher *common.MatchDefinition) ([]iface.Serviceable, error) {
	return []iface.Serviceable{}, nil // Empty picks
}

type mockTnsClientWithValidPath struct {
	tns.Client
}

func (m *mockTnsClientWithValidPath) Fetch(path tns.Path) (tns.Object, error) {
	return &mockTnsObjectWithValidPath{}, nil
}

type mockTnsObjectWithValidPath struct{}

func (m *mockTnsObjectWithValidPath) Interface() interface{} {
	// Return []interface{} with valid string paths
	return []interface{}{"valid/project/application/path"}
}

func (m *mockTnsObjectWithValidPath) Bind(interface{}) error               { return nil }
func (m *mockTnsObjectWithValidPath) Current([]string) ([]tns.Path, error) { return nil, nil }
func (m *mockTnsObjectWithValidPath) Path() tns.Path                       { return &mockTnsPath{} }

type mockTnsPath struct{}

func (m *mockTnsPath) String() string  { return "test-path" }
func (m *mockTnsPath) Slice() []string { return []string{"test", "path"} }
