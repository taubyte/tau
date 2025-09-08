package websocket

import (
	"context"
	"testing"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
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

		if string(msg.Message) != "test message" {
			t.Errorf("Expected message 'test message', got '%s'", string(msg.Message))
		}

		if msg.Error != "" {
			t.Errorf("Expected empty error, got '%s'", msg.Error)
		}
	})

	t.Run("create wrapped message with error", func(t *testing.T) {
		msg := WrappedMessage{
			Message: []byte(""),
			Error:   "test error",
		}

		if len(msg.Message) != 0 {
			t.Errorf("Expected empty message, got '%s'", string(msg.Message))
		}

		if msg.Error != "test error" {
			t.Errorf("Expected error 'test error', got '%s'", msg.Error)
		}
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
			if id != i {
				t.Errorf("Expected id %d, got %d", i, id)
			}
		}

		// Verify nextId was incremented
		if sv.nextId != 5 {
			t.Errorf("Expected nextId to be 5, got %d", sv.nextId)
		}
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

		if !handler1Called {
			t.Error("Expected handler1 to be called")
		}
		if !handler2Called {
			t.Error("Expected handler2 to be called")
		}
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

		if !errHandler1Called {
			t.Error("Expected errHandler1 to be called")
		}
		if !errHandler2Called {
			t.Error("Expected errHandler2 to be called")
		}
	})
}

func TestSubsViewer(t *testing.T) {
	t.Run("initialization", func(t *testing.T) {
		sv := &subsViewer{
			subscriptions: make(map[string]*subViewer),
		}

		if sv.subscriptions == nil {
			t.Error("Expected subscriptions map to be initialized")
		}

		if len(sv.subscriptions) != 0 {
			t.Errorf("Expected empty subscriptions map, got %d items", len(sv.subscriptions))
		}
	})
}
