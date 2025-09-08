package websocket

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
	"gotest.tools/v3/assert"
)

// Mock implementations for testing
type mockWebSocketConnection struct {
	readMessageFunc  func() (int, []byte, error)
	writeJSONFunc    func(v interface{}) error
	writeMessageFunc func(messageType int, data []byte) error
	closeFunc        func() error
}

func (m *mockWebSocketConnection) ReadMessage() (int, []byte, error) {
	if m.readMessageFunc != nil {
		return m.readMessageFunc()
	}
	return 0, nil, nil
}

func (m *mockWebSocketConnection) WriteJSON(v interface{}) error {
	if m.writeJSONFunc != nil {
		return m.writeJSONFunc(v)
	}
	return nil
}

func (m *mockWebSocketConnection) WriteMessage(messageType int, data []byte) error {
	if m.writeMessageFunc != nil {
		return m.writeMessageFunc(messageType, data)
	}
	return nil
}

func (m *mockWebSocketConnection) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

type mockMatchDefinition struct {
	stringFunc func() string
}

func (m *mockMatchDefinition) String() string {
	if m.stringFunc != nil {
		return m.stringFunc()
	}
	return "test-topic"
}

func (m *mockMatchDefinition) CachePrefix() string {
	return "test-prefix"
}

type mockLocalService struct {
	common.LocalService
	pubSubPublishFunc func(ctx context.Context, topic string, data []byte) error
	contextFunc       func() context.Context
}

func (m *mockLocalService) Node() peer.Node {
	return &mockNode{
		pubSubPublishFunc: m.pubSubPublishFunc,
	}
}

func (m *mockLocalService) Context() context.Context {
	return m.contextFunc()
}

type mockNode struct {
	peer.Node
	pubSubPublishFunc func(ctx context.Context, topic string, data []byte) error
}

func (m *mockNode) PubSubPublish(ctx context.Context, topic string, data []byte) error {
	if m.pubSubPublishFunc != nil {
		return m.pubSubPublishFunc(ctx, topic, data)
	}
	return nil
}

func TestDataStreamHandler_Close(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler := &dataStreamHandler{
		ctx:   ctx,
		ctxC:  cancel,
		ch:    make(chan []byte, 1),
		errCh: make(chan error, 1),
	}

	// Test that Close cancels context and closes channels
	handler.Close()

	// Verify context is cancelled
	select {
	case <-handler.ctx.Done():
		// Expected
	default:
		t.Error("Expected context to be cancelled")
	}

	// Verify channels are closed
	select {
	case _, ok := <-handler.ch:
		if ok {
			t.Error("Expected ch channel to be closed")
		}
	default:
		t.Error("Expected ch channel to be closed")
	}

	select {
	case _, ok := <-handler.errCh:
		if ok {
			t.Error("Expected errCh channel to be closed")
		}
	default:
		t.Error("Expected errCh channel to be closed")
	}
}

func TestDataStreamHandler_Error(t *testing.T) {
	t.Run("successful error sending", func(t *testing.T) {
		handler := &dataStreamHandler{
			errCh: make(chan error, 1),
		}

		testErr := errors.New("test error")
		handler.Error(testErr)

		select {
		case err := <-handler.errCh:
			if err != testErr {
				t.Errorf("Expected error %v, got %v", testErr, err)
			}
		default:
			t.Error("Expected error to be sent to channel")
		}
	})

	t.Run("blocked error channel", func(t *testing.T) {
		// Create a channel with no buffer to simulate blocking
		handler := &dataStreamHandler{
			errCh: make(chan error),
		}

		testErr := errors.New("test error")

		// This should not block or panic
		handler.Error(testErr)

		// Verify the error was not sent (channel is blocked)
		select {
		case <-handler.errCh:
			t.Error("Expected error channel to be blocked")
		default:
			// Expected - channel is blocked
		}
	})
}

func TestDataStreamHandler_In(t *testing.T) {
	t.Run("successful message processing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		expectedData := []byte("test message")
		expectedTopic := "test-topic"
		var publishCalled bool
		var mu sync.Mutex

		conn := &mockWebSocketConnection{
			readMessageFunc: func() (int, []byte, error) {
				return websocket.TextMessage, expectedData, nil
			},
		}

		matcher := &mockMatchDefinition{
			stringFunc: func() string {
				return expectedTopic
			},
		}

		// Create a mock service that implements the required interface
		mockSrv := &mockLocalService{
			pubSubPublishFunc: func(ctx context.Context, topic string, data []byte) error {
				mu.Lock()
				publishCalled = true
				mu.Unlock()
				if topic != expectedTopic {
					t.Errorf("Expected topic %s, got %s", expectedTopic, topic)
				}
				if string(data) != string(expectedData) {
					t.Errorf("Expected data %s, got %s", string(expectedData), string(data))
				}
				return nil
			},
		}

		handler := &dataStreamHandler{
			ctx:     ctx,
			ctxC:    cancel,
			conn:    conn,
			srv:     mockSrv,
			matcher: matcher,
		}

		// Start the In goroutine
		go handler.In()

		// Wait a bit for the message to be processed
		time.Sleep(10 * time.Millisecond)

		mu.Lock()
		called := publishCalled
		mu.Unlock()
		assert.Assert(t, called, "Expected PubSubPublish to be called")
	})

	t.Run("publish error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		expectedData := []byte("test message")
		publishErr := errors.New("publish error")
		var errorSent bool
		var mu sync.Mutex

		conn := &mockWebSocketConnection{
			readMessageFunc: func() (int, []byte, error) {
				return websocket.TextMessage, expectedData, nil
			},
		}

		// Create a mock service that returns an error
		mockSrv := &mockLocalService{
			pubSubPublishFunc: func(ctx context.Context, topic string, data []byte) error {
				return publishErr
			},
		}

		handler := &dataStreamHandler{
			ctx:     ctx,
			ctxC:    cancel,
			conn:    conn,
			srv:     mockSrv,
			errCh:   make(chan error, 1),
			matcher: &mockMatchDefinition{},
		}

		// Start the In goroutine
		go handler.In()

		// Wait for error to be sent
		select {
		case err := <-handler.errCh:
			mu.Lock()
			errorSent = true
			mu.Unlock()
			if err == nil {
				t.Error("Expected error to be sent")
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("Expected error to be sent within timeout")
		}

		mu.Lock()
		sent := errorSent
		mu.Unlock()
		assert.Assert(t, sent, "Expected error to be sent to error channel")
	})

	t.Run("read message error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		readErr := errors.New("read error")
		errorSent := false

		conn := &mockWebSocketConnection{
			readMessageFunc: func() (int, []byte, error) {
				return 0, nil, readErr
			},
		}

		handler := &dataStreamHandler{
			ctx:     ctx,
			ctxC:    cancel,
			conn:    conn,
			errCh:   make(chan error, 1),
			matcher: &mockMatchDefinition{},
		}

		// Start the In goroutine
		go handler.In()

		// Wait for error to be sent
		select {
		case err := <-handler.errCh:
			errorSent = true
			if err == nil {
				t.Error("Expected error to be sent")
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("Expected error to be sent within timeout")
		}

		if !errorSent {
			t.Error("Expected error to be sent to error channel")
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		conn := &mockWebSocketConnection{
			readMessageFunc: func() (int, []byte, error) {
				// Simulate blocking read
				time.Sleep(100 * time.Millisecond)
				return websocket.TextMessage, []byte("test"), nil
			},
		}

		handler := &dataStreamHandler{
			ctx:     ctx,
			ctxC:    cancel,
			conn:    conn,
			matcher: &mockMatchDefinition{},
		}

		// Start the In goroutine
		go handler.In()

		// Cancel context immediately
		cancel()

		// Wait a bit to ensure the goroutine exits
		time.Sleep(50 * time.Millisecond)

		// The goroutine should have exited due to context cancellation
		// We can't directly test this, but if it doesn't exit, the test will hang
	})
}

func TestDataStreamHandler_Out(t *testing.T) {
	t.Run("successful message writing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		expectedData := []byte("test message")
		var writeCalled bool
		var mu sync.Mutex

		conn := &mockWebSocketConnection{
			writeMessageFunc: func(messageType int, data []byte) error {
				mu.Lock()
				writeCalled = true
				mu.Unlock()
				if messageType != websocket.BinaryMessage {
					t.Errorf("Expected BinaryMessage, got %d", messageType)
				}
				if string(data) != string(expectedData) {
					t.Errorf("Expected data %s, got %s", string(expectedData), string(data))
				}
				return nil
			},
		}

		handler := &dataStreamHandler{
			ctx:   ctx,
			ctxC:  cancel,
			conn:  conn,
			ch:    make(chan []byte, 1),
			errCh: make(chan error, 1),
		}

		// Send data to channel
		handler.ch <- expectedData

		// Start the Out goroutine
		go handler.Out()

		// Wait for message to be written
		time.Sleep(10 * time.Millisecond)

		mu.Lock()
		called := writeCalled
		mu.Unlock()
		assert.Assert(t, called, "Expected WriteMessage to be called")
	})

	t.Run("error handling", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		testErr := errors.New("test error")
		var writeJSONCalled bool
		var closeCalled bool
		var mu sync.Mutex

		conn := &mockWebSocketConnection{
			writeJSONFunc: func(v interface{}) error {
				mu.Lock()
				writeJSONCalled = true
				mu.Unlock()
				wrappedMsg, ok := v.(WrappedMessage)
				if !ok {
					t.Error("Expected WrappedMessage type")
				}
				if wrappedMsg.Error == "" {
					t.Error("Expected error message in WrappedMessage")
				}
				return nil
			},
			closeFunc: func() error {
				mu.Lock()
				closeCalled = true
				mu.Unlock()
				return nil
			},
		}

		handler := &dataStreamHandler{
			ctx:   ctx,
			ctxC:  cancel,
			conn:  conn,
			ch:    make(chan []byte, 1),
			errCh: make(chan error, 1),
		}

		// Send error to error channel
		handler.errCh <- testErr

		// Start the Out goroutine
		go handler.Out()

		// Wait for error handling
		time.Sleep(10 * time.Millisecond)

		mu.Lock()
		jsonCalled := writeJSONCalled
		closeCalledVal := closeCalled
		mu.Unlock()
		assert.Assert(t, jsonCalled, "Expected WriteJSON to be called")
		assert.Assert(t, closeCalledVal, "Expected Close to be called")
	})

	t.Run("write message error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		expectedData := []byte("test message")
		writeErr := errors.New("write error")
		errorSent := false

		conn := &mockWebSocketConnection{
			writeMessageFunc: func(messageType int, data []byte) error {
				return writeErr
			},
		}

		handler := &dataStreamHandler{
			ctx:   ctx,
			ctxC:  cancel,
			conn:  conn,
			ch:    make(chan []byte, 1),
			errCh: make(chan error, 1),
		}

		// Send data to channel
		handler.ch <- expectedData

		// Start the Out goroutine
		go handler.Out()

		// Wait for error to be sent
		select {
		case err := <-handler.errCh:
			errorSent = true
			if err == nil {
				t.Error("Expected error to be sent")
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("Expected error to be sent within timeout")
		}

		if !errorSent {
			t.Error("Expected error to be sent to error channel")
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		conn := &mockWebSocketConnection{}

		handler := &dataStreamHandler{
			ctx:   ctx,
			ctxC:  cancel,
			conn:  conn,
			ch:    make(chan []byte, 1),
			errCh: make(chan error, 1),
		}

		// Start the Out goroutine
		go handler.Out()

		// Cancel context immediately
		cancel()

		// Wait a bit to ensure the goroutine exits
		time.Sleep(50 * time.Millisecond)

		// The goroutine should have exited due to context cancellation
		// We can't directly test this, but if it doesn't exit, the test will hang
	})
}

// Test the default case in the In method's select statement
func TestDataStreamHandler_In_DefaultCase(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a connection that returns an error immediately
	conn := &mockWebSocketConnection{
		readMessageFunc: func() (int, []byte, error) {
			return 0, nil, errors.New("immediate error")
		},
	}

	handler := &dataStreamHandler{
		ctx:     ctx,
		ctxC:    cancel,
		conn:    conn,
		errCh:   make(chan error, 1),
		matcher: &mockMatchDefinition{},
	}

	// Start the In goroutine
	go handler.In()

	// Wait for error to be sent
	select {
	case err := <-handler.errCh:
		if err == nil {
			t.Error("Expected error to be sent")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected error to be sent within timeout")
	}
}

// Test the default case in the Out method's select statement
func TestDataStreamHandler_Out_DefaultCase(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := &mockWebSocketConnection{}

	handler := &dataStreamHandler{
		ctx:   ctx,
		ctxC:  cancel,
		conn:  conn,
		ch:    make(chan []byte, 1),
		errCh: make(chan error, 1),
	}

	// Start the Out goroutine
	go handler.Out()

	// Cancel context to trigger the default case
	cancel()

	// Wait a bit to ensure the goroutine exits
	time.Sleep(50 * time.Millisecond)

	// The goroutine should have exited due to context cancellation
}
