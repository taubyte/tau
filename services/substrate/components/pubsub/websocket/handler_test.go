package websocket

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	pubsubMsg "github.com/libp2p/go-libp2p-pubsub"
	"github.com/taubyte/tau/core/services/substrate/components"
	iface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/core/services/tns"
	p2p "github.com/taubyte/tau/p2p/peer"
	service "github.com/taubyte/tau/pkg/http"
	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
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

// Helper function to create a mock service for handler tests
func createMockServiceForHandler() *mockLocalService {
	return &mockLocalService{
		contextFunc:   func() context.Context { return context.Background() },
		tnsClientFunc: func() tns.Client { return &mockTnsClient{} },
		lookupFunc: func(matcher *common.MatchDefinition) ([]iface.Serviceable, error) {
			return nil, errors.New("lookup failed")
		},
	}
}

// Helper function to create a mock service for AddSubscription error tests
func createMockServiceForAddSubscriptionError() *mockLocalService {
	return &mockLocalService{
		contextFunc:   func() context.Context { return context.Background() },
		tnsClientFunc: func() tns.Client { return &mockTnsClientWithValidPath{} },
		lookupFunc: func(matcher *common.MatchDefinition) ([]iface.Serviceable, error) {
			return []iface.Serviceable{&mockWebSocket{
				project: "test-project",
				matcher: matcher,
				mmi:     common.MessagingMapItem{},
				commit:  "mock-commit",
				branch:  "mock-branch",
			}}, nil
		},
		pubSubSubscribeFunc: func(topic string, handler p2p.PubSubConsumerHandler, errHandler p2p.PubSubConsumerErrorHandler) error {
			return errors.New("pubsub subscribe failed")
		},
	}
}

// Helper function to create a mock service for lookup error tests
func createMockServiceForLookupError() *mockLocalService {
	return &mockLocalService{
		contextFunc:   func() context.Context { return context.Background() },
		tnsClientFunc: func() tns.Client { return &mockTnsClientWithValidPath{} },
		lookupFunc: func(matcher *common.MatchDefinition) ([]iface.Serviceable, error) {
			return nil, errors.New("lookup failed")
		},
	}
}

// Helper function to create a mock service for empty picks tests
func createMockServiceForEmptyPicks() *mockLocalService {
	return &mockLocalService{
		contextFunc:   func() context.Context { return context.Background() },
		tnsClientFunc: func() tns.Client { return &mockTnsClientWithValidPath{} },
		lookupFunc: func(matcher *common.MatchDefinition) ([]iface.Serviceable, error) {
			return []iface.Serviceable{}, nil // Empty picks
		},
	}
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
	runHandlerTest(t, "handles createWsHandler error",
		createMockServiceContext("", "", errors.New("hash not found"), nil),
		createMockServiceForHandler(),
		true, "Expected handler to be nil due to error")
}

// Helper function to create a mock service context
func createMockServiceContext(hash, channel string, hashErr, channelErr error) *mockServiceContext {
	return &mockServiceContext{
		getStringVariableFunc: func(key string) (string, error) {
			switch key {
			case "hash":
				return hash, hashErr
			case "channel":
				return channel, channelErr
			default:
				return "", errors.New("unknown key")
			}
		},
	}
}

// Helper function to create a mock websocket connection
func createMockWebSocketConnection() *mockWebSocketConnection {
	return &mockWebSocketConnection{
		writeJSONFunc: func(v interface{}) error { return nil },
		closeFunc:     func() error { return nil },
	}
}

// Helper function to run a handler test with common setup
func runHandlerTest(t *testing.T, testName string, ctx *mockServiceContext, srv common.LocalService, expectedNil bool, expectedMsg string) {
	t.Run(testName, func(t *testing.T) {
		conn := createMockWebSocketConnection()
		result := Handler(srv, ctx, conn)

		if expectedNil {
			assert.Assert(t, result == nil, expectedMsg)
		} else {
			assert.Assert(t, result != nil, expectedMsg)
		}
	})
}

func TestHandler_CreateWsHandlerErrorPaths(t *testing.T) {
	// Reset global subs for testing
	subs = &subsViewer{
		subscriptions: make(map[string]*subViewer),
	}

	runHandlerTest(t, "missing hash variable",
		createMockServiceContext("", "", errors.New("hash not found"), nil),
		createMockServiceForHandler(),
		true, "Expected handler to be nil due to hash error")

	runHandlerTest(t, "missing channel variable",
		createMockServiceContext("test-hash", "", nil, errors.New("channel not found")),
		createMockServiceForHandler(),
		true, "Expected handler to be nil due to channel error")

	runHandlerTest(t, "messagingSpec.Tns().WebSocketPath error",
		createMockServiceContext("", "test-channel", nil, nil), // Empty hash causes WebSocketPath to fail
		createMockServiceForHandler(),
		true, "Expected handler to be nil due to WebSocketPath error")

	runHandlerTest(t, "TNS fetch error",
		createMockServiceContext("test-hash", "test-channel", nil, nil),
		createMockServiceForHandler(),
		true, "Expected handler to be nil due to TNS fetch error")

	runHandlerTest(t, "AddSubscription error",
		createMockServiceContext("valid-hash", "test-channel", nil, nil),
		createMockServiceForAddSubscriptionError(),
		true, "Expected handler to be nil due to AddSubscription error")
}

func TestCreateWsHandler_AdditionalErrorPaths(t *testing.T) {
	// messagingSpec.Tns().WebSocketPath error is already tested above with empty hash

	runHandlerTest(t, "lookup error",
		createMockServiceContext("valid-hash", "test-channel", nil, nil),
		createMockServiceForLookupError(),
		true, "Expected handler to be nil due to lookup error")

	runHandlerTest(t, "empty picks from lookup",
		createMockServiceContext("valid-hash", "test-channel", nil, nil),
		createMockServiceForEmptyPicks(),
		true, "Expected handler to be nil due to empty picks")
}

// Mock TNS client with valid path for tests that need to succeed up to lookup
type mockTnsClientWithValidPath struct {
	tns.Client
}

func (m *mockTnsClientWithValidPath) Fetch(path tns.Path) (tns.Object, error) {
	return &mockTnsObjectWithValidPath{}, nil
}

type mockTnsObjectWithValidPath struct{}

func (m *mockTnsObjectWithValidPath) Interface() interface{} {
	// Return []interface{} with valid string paths that match the expected format for extract.Tns().BasicPath
	return []interface{}{"branches/master/projects/test-project/applications/test-app/messaging/test-message"}
}

func (m *mockTnsObjectWithValidPath) Bind(interface{}) error               { return nil }
func (m *mockTnsObjectWithValidPath) Current([]string) ([]tns.Path, error) { return nil, nil }
func (m *mockTnsObjectWithValidPath) Path() tns.Path                       { return &mockTnsPath{} }

type mockTnsPath struct{}

func (m *mockTnsPath) String() string  { return "test-path" }
func (m *mockTnsPath) Slice() []string { return []string{"test", "path"} }

// Mock WebSocket for testing
type mockWebSocket struct {
	project string
	matcher *common.MatchDefinition
	mmi     common.MessagingMapItem
	commit  string
	branch  string
	ctxC    context.CancelFunc
	srv     common.LocalService
}

func (m *mockWebSocket) HandleMessage(msg *pubsubMsg.Message) (time.Time, error) {
	return time.Now(), nil
}

func (m *mockWebSocket) Name() string {
	return "mock-websocket"
}

func (m *mockWebSocket) Project() string {
	return m.project
}

func (m *mockWebSocket) Application() string {
	return m.matcher.Application
}

func (m *mockWebSocket) Config() *structureSpec.Function {
	return &structureSpec.Function{}
}

func (m *mockWebSocket) Match(def components.MatchDefinition) matcherSpec.Index {
	return 0
}

func (m *mockWebSocket) Validate(def components.MatchDefinition) error {
	return nil
}

func (m *mockWebSocket) Matcher() components.MatchDefinition {
	return m.matcher
}

func (m *mockWebSocket) Ready() error {
	return nil
}

func (m *mockWebSocket) Id() string {
	return "mock-websocket-id"
}

func (m *mockWebSocket) Commit() string {
	return m.commit
}

func (m *mockWebSocket) Branch() string {
	return m.branch
}

func (m *mockWebSocket) AssetId() string {
	return "mock-asset-id"
}

func (m *mockWebSocket) Service() components.ServiceComponent {
	return m.srv
}

func (m *mockWebSocket) Close() {
	if m.ctxC != nil {
		m.ctxC()
	}
}

func (m *mockWebSocket) Clean() {
	m.Close()
}
