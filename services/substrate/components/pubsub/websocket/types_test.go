package websocket

import (
	"context"
	"testing"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"gotest.tools/v3/assert"
)

func TestWebSocket_Close(t *testing.T) {
	t.Run("successful close", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		ws := &WebSocket{
			ctx:  ctx,
			ctxC: cancel,
		}

		// Call Close - this should cancel the context
		ws.Close()

		// Verify context is cancelled
		select {
		case <-ws.ctx.Done():
			// Expected - context should be cancelled
		default:
			t.Error("Expected context to be cancelled after Close()")
		}
	})

	t.Run("close with nil cancel function", func(t *testing.T) {
		ctx, _ := context.WithCancel(context.Background())

		ws := &WebSocket{
			ctx:  ctx,
			ctxC: nil,
		}

		// This will panic due to nil pointer dereference
		// We expect this behavior based on the current implementation
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic due to nil cancel function")
			}
		}()

		ws.Close()
	})
}

func TestWrappedMessage(t *testing.T) {
	t.Run("create wrapped message with data", func(t *testing.T) {
		msg := WrappedMessage{
			Message: []byte("test message"),
			Error:   "",
		}

		assert.Equal(t, string(msg.Message), "test message")
		assert.Equal(t, msg.Error, "")
	})

	t.Run("create wrapped message with error", func(t *testing.T) {
		msg := WrappedMessage{
			Message: []byte(""),
			Error:   "test error",
		}

		assert.Equal(t, len(msg.Message), 0)
		assert.Equal(t, msg.Error, "test error")
	})
}

func TestSubViewer(t *testing.T) {
	t.Run("getNextId increments correctly", func(t *testing.T) {
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

	t.Run("handler with multiple subscriptions", func(t *testing.T) {
		sv := &subViewer{
			subs:   make(map[int]*sub),
			nextId: 0,
		}

		// Add some mock subscriptions
		handler1Called := false
		handler2Called := false

		sv.subs[1] = &sub{
			handler: func(msg *pubsub.Message) {
				handler1Called = true
			},
		}

		sv.subs[2] = &sub{
			handler: func(msg *pubsub.Message) {
				handler2Called = true
			},
		}

		// Call handler
		sv.handler(&pubsub.Message{})

		assert.Assert(t, handler1Called, "Expected handler1 to be called")
		assert.Assert(t, handler2Called, "Expected handler2 to be called")
	})

	t.Run("err_handler with multiple subscriptions", func(t *testing.T) {
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
}

func TestSubsViewer(t *testing.T) {
	t.Run("initialization", func(t *testing.T) {
		sv := &subsViewer{
			subscriptions: make(map[string]*subViewer),
		}

		assert.Assert(t, sv.subscriptions != nil, "Expected subscriptions map to be initialized")
		assert.Equal(t, len(sv.subscriptions), 0)
	})
}
