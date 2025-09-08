package websocket

import (
	"testing"

	pubsubMsg "github.com/libp2p/go-libp2p-pubsub"
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

// Note: AddSubscription tests are complex due to dependencies on pubsub subscription
// For now, we'll test the simpler functions that don't require complex mocking
