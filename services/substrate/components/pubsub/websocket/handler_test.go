package websocket

import (
	"context"
	"errors"
	"testing"

	pubsubMsg "github.com/libp2p/go-libp2p-pubsub"
	p2p "github.com/taubyte/tau/p2p/peer"
	"gotest.tools/v3/assert"
)

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
		// This test is complex due to dependencies, but we can test the basic structure
		// For now, we'll focus on other functions that are easier to test
		t.Skip("Handler function requires complex mocking of service.Context and websocket.Conn")
	})
}
