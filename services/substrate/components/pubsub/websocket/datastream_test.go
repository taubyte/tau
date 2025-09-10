package websocket

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	p2p "github.com/taubyte/tau/p2p/peer"
	service "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
	"gotest.tools/v3/assert"
)

// Mock implementations for testing
type mockWebSocketConnection struct {
	readMessageFunc            func() (int, []byte, error)
	writeJSONFunc              func(v interface{}) error
	writeMessageFunc           func(messageType int, data []byte) error
	closeFunc                  func() error
	enableWriteCompressionFunc func(enable bool)
	setCloseHandlerFunc        func(handler func(code int, text string) error)
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

func (m *mockWebSocketConnection) EnableWriteCompression(enable bool) {
	if m.enableWriteCompressionFunc != nil {
		m.enableWriteCompressionFunc(enable)
	}
}

func (m *mockWebSocketConnection) SetCloseHandler(handler func(code int, text string) error) {
	if m.setCloseHandlerFunc != nil {
		m.setCloseHandlerFunc(handler)
	}
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
	pubSubPublishFunc   func(ctx context.Context, topic string, data []byte) error
	pubSubSubscribeFunc func(topic string, handler p2p.PubSubConsumerHandler, errHandler p2p.PubSubConsumerErrorHandler) error
	contextFunc         func() context.Context
}

func (m *mockLocalService) Node() p2p.Node {
	return &mockNode{
		pubSubPublishFunc:   m.pubSubPublishFunc,
		pubSubSubscribeFunc: m.pubSubSubscribeFunc,
	}
}

func (m *mockLocalService) Context() context.Context {
	return m.contextFunc()
}

type mockNode struct {
	p2p.Node
	pubSubPublishFunc   func(ctx context.Context, topic string, data []byte) error
	pubSubSubscribeFunc func(topic string, handler p2p.PubSubConsumerHandler, errHandler p2p.PubSubConsumerErrorHandler) error
}

func (m *mockNode) PubSubPublish(ctx context.Context, topic string, data []byte) error {
	if m.pubSubPublishFunc != nil {
		return m.pubSubPublishFunc(ctx, topic, data)
	}
	return nil
}

func (m *mockNode) PubSubSubscribe(topic string, handler p2p.PubSubConsumerHandler, errHandler p2p.PubSubConsumerErrorHandler) error {
	if m.pubSubSubscribeFunc != nil {
		return m.pubSubSubscribeFunc(topic, handler, errHandler)
	}
	return nil
}

// Helper functions to reduce test duplication
func createTestHandler(ctx context.Context, cancel context.CancelFunc, conn service.WebSocketConnection, srv common.LocalService) *dataStreamHandler {
	return &dataStreamHandler{
		ctx:     ctx,
		ctxC:    cancel,
		conn:    conn,
		ch:      make(chan []byte, 1),
		errCh:   make(chan error, 1),
		matcher: &mockMatchDefinition{},
		srv:     srv,
	}
}

func createMockService(publishFunc func(ctx context.Context, topic string, data []byte) error) *mockLocalService {
	return &mockLocalService{
		pubSubPublishFunc: publishFunc,
	}
}

func createMockConnection(readFunc func() (int, []byte, error), writeFunc func(messageType int, data []byte) error) *mockWebSocketConnection {
	return &mockWebSocketConnection{
		readMessageFunc:  readFunc,
		writeMessageFunc: writeFunc,
	}
}

// Helper function to create a mock connection that reads successfully
func createSuccessfulReadConnection(data []byte) *mockWebSocketConnection {
	return createMockConnection(
		func() (int, []byte, error) {
			return websocket.TextMessage, data, nil
		},
		nil,
	)
}

// Helper function to create a mock connection that returns read error
func createReadErrorConnection(err error) *mockWebSocketConnection {
	return createMockConnection(
		func() (int, []byte, error) {
			return 0, nil, err
		},
		nil,
	)
}

// Helper function to create a mock connection that writes successfully
func createSuccessfulWriteConnection() *mockWebSocketConnection {
	return createMockConnection(
		nil,
		func(messageType int, data []byte) error {
			return nil
		},
	)
}

// Helper function to create a mock connection that returns write error
func createWriteErrorConnection(err error) *mockWebSocketConnection {
	return createMockConnection(
		nil,
		func(messageType int, data []byte) error {
			return err
		},
	)
}

func verifyChannelsClosed(t *testing.T, handler *dataStreamHandler) {
	assert.Assert(t, handler.ch == nil, "Expected ch to be nil")
	assert.Assert(t, handler.errCh == nil, "Expected errCh to be nil")
}

func verifyContextCancelled(t *testing.T, handler *dataStreamHandler) {
	select {
	case <-handler.ctx.Done():
		// Expected
	default:
		t.Error("Expected context to be cancelled")
	}
}

// Helper function to run a test with proper setup and cleanup
func runTestWithHandler(t *testing.T, testName string, testFunc func(t *testing.T, handler *dataStreamHandler, conn *mockWebSocketConnection)) {
	t.Run(testName, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		conn := &mockWebSocketConnection{}
		handler := createTestHandler(ctx, cancel, conn, createMockService(nil))

		testFunc(t, handler, conn)
	})
}

func TestDataStreamHandler_Close(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler := createTestHandler(ctx, cancel, &mockWebSocketConnection{}, createMockService(nil))

	// Test that Close cancels context and closes channels
	handler.Close()

	verifyContextCancelled(t, handler)
	verifyChannelsClosed(t, handler)
}

func TestDataStreamHandler_Error(t *testing.T) {
	t.Run("successful error sending", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		handler := createTestHandler(ctx, cancel, &mockWebSocketConnection{}, createMockService(nil))

		testErr := errors.New("test error")
		handler.error(testErr)

		select {
		case err := <-handler.errCh:
			assert.Equal(t, err, testErr)
		default:
			t.Error("Expected error to be sent to channel")
		}
	})

	t.Run("blocked error channel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create handler with unbuffered error channel
		handler := &dataStreamHandler{
			ctx:   ctx,
			ctxC:  cancel,
			errCh: make(chan error), // No buffer
		}

		testErr := errors.New("test error")

		// This should not block or panic due to select with default
		handler.error(testErr)

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

		conn := createMockConnection(
			func() (int, []byte, error) {
				return websocket.TextMessage, expectedData, nil
			},
			nil,
		)

		matcher := &mockMatchDefinition{
			stringFunc: func() string {
				return expectedTopic
			},
		}

		mockSrv := createMockService(func(ctx context.Context, topic string, data []byte) error {
			mu.Lock()
			publishCalled = true
			mu.Unlock()
			assert.Equal(t, topic, expectedTopic)
			assert.Equal(t, string(data), string(expectedData))
			return nil
		})

		handler := createTestHandler(ctx, cancel, conn, mockSrv)
		handler.matcher = matcher

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

		conn := createSuccessfulReadConnection(expectedData)

		mockSrv := createMockService(func(ctx context.Context, topic string, data []byte) error {
			return publishErr
		})

		handler := createTestHandler(ctx, cancel, conn, mockSrv)

		// Start the In goroutine
		go handler.In()

		// Wait for error to be sent
		select {
		case err := <-handler.errCh:
			mu.Lock()
			errorSent = true
			mu.Unlock()
			assert.Assert(t, err != nil, "Expected error to be sent")
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

		conn := createReadErrorConnection(readErr)

		handler := createTestHandler(ctx, cancel, conn, createMockService(nil))

		// Start the In goroutine
		go handler.In()

		// Wait for error to be sent
		select {
		case err := <-handler.errCh:
			errorSent = true
			assert.Assert(t, err != nil, "Expected error to be sent")
		case <-time.After(100 * time.Millisecond):
			t.Error("Expected error to be sent within timeout")
		}

		assert.Assert(t, errorSent, "Expected error to be sent to error channel")
	})

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		conn := createMockConnection(
			func() (int, []byte, error) {
				// Simulate blocking read
				time.Sleep(100 * time.Millisecond)
				return websocket.TextMessage, []byte("test"), nil
			},
			nil,
		)

		handler := createTestHandler(ctx, cancel, conn, createMockService(nil))

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

		conn := createMockConnection(
			nil,
			func(messageType int, data []byte) error {
				mu.Lock()
				writeCalled = true
				mu.Unlock()
				assert.Equal(t, messageType, websocket.BinaryMessage)
				assert.Equal(t, string(data), string(expectedData))
				return nil
			},
		)

		handler := createTestHandler(ctx, cancel, conn, createMockService(nil))

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
				assert.Assert(t, ok, "Expected WrappedMessage type")
				assert.Assert(t, wrappedMsg.Error != "", "Expected error message in WrappedMessage")
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
			assert.Assert(t, err != nil, "Expected error to be sent")
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

// Test for race conditions and closed channel writes
func TestDataStreamHandler_RaceConditions(t *testing.T) {
	t.Run("concurrent close and channel operations", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create handler with small buffer to increase chance of race conditions
		handler := &dataStreamHandler{
			ctx:   ctx,
			ctxC:  cancel,
			ch:    make(chan []byte, 1),
			errCh: make(chan error, 1),
			conn:  &mockWebSocketConnection{},
			srv:   &mockLocalService{},
		}

		// Start In and Out goroutines
		go handler.In()
		go handler.Out()

		// Try to close while goroutines are running
		// This should not panic or cause issues
		handler.Close()

		// Verify channels are closed
		verifyChannelsClosed(t, handler)
	})

	t.Run("write to closed channel after close", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create a connection that will return an error on read
		conn := &mockWebSocketConnection{
			readMessageFunc: func() (int, []byte, error) {
				return 0, nil, errors.New("connection error")
			},
		}

		handler := &dataStreamHandler{
			ctx:     ctx,
			ctxC:    cancel,
			conn:    conn,
			ch:      make(chan []byte, 1),
			errCh:   make(chan error, 1),
			matcher: &mockMatchDefinition{},
			srv: &mockLocalService{
				pubSubPublishFunc: func(ctx context.Context, topic string, data []byte) error {
					return nil
				},
			},
		}

		// Start In goroutine - it should handle the error gracefully
		go handler.In()

		// Wait a bit for the error to be processed
		time.Sleep(10 * time.Millisecond)

		// Close the handler
		handler.Close()

		// This should not panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Unexpected panic when closing handler: %v", r)
			}
		}()
	})

	t.Run("concurrent multiple close calls", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		handler := &dataStreamHandler{
			ctx:   ctx,
			ctxC:  cancel,
			ch:    make(chan []byte, 1),
			errCh: make(chan error, 1),
		}

		// Call Close multiple times concurrently
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				handler.Close()
			}()
		}

		wg.Wait()

		verifyChannelsClosed(t, handler)
	})

	t.Run("error channel blocking behavior", func(t *testing.T) {
		// Test that error handling doesn't block when error channel is full
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create a connection that will cause an error
		conn := &mockWebSocketConnection{
			readMessageFunc: func() (int, []byte, error) {
				return 0, nil, errors.New("read error")
			},
		}

		handler := &dataStreamHandler{
			ctx:     ctx,
			ctxC:    cancel,
			conn:    conn,
			ch:      make(chan []byte, 1),
			errCh:   make(chan error, 1), // Small buffer
			matcher: &mockMatchDefinition{},
			srv: &mockLocalService{
				pubSubPublishFunc: func(ctx context.Context, topic string, data []byte) error {
					return nil
				},
			},
		}

		// Fill the error channel first
		handler.errCh <- errors.New("first error")

		// Start In goroutine - it should handle the read error gracefully
		// even when error channel is full
		go handler.In()

		// Wait a bit for processing
		time.Sleep(10 * time.Millisecond)

		// Verify only one error was in the channel (the first one)
		errorCount := 0
		select {
		case <-handler.errCh:
			errorCount++
		default:
		}

		assert.Equal(t, errorCount, 1)
	})
}

// Test specific scenarios that could cause closed channel writes
func TestDataStreamHandler_ClosedChannelWrites(t *testing.T) {
	t.Run("In method with publish errors", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create a mock connection that will read successfully
		conn := &mockWebSocketConnection{
			readMessageFunc: func() (int, []byte, error) {
				return websocket.TextMessage, []byte("test"), nil
			},
		}

		// Create a service that will return an error on publish
		handler := &dataStreamHandler{
			ctx:     ctx,
			ctxC:    cancel,
			conn:    conn,
			ch:      make(chan []byte, 1),
			errCh:   make(chan error, 1),
			matcher: &mockMatchDefinition{},
			srv: &mockLocalService{
				pubSubPublishFunc: func(ctx context.Context, topic string, data []byte) error {
					return errors.New("publish error")
				},
			},
		}

		// Start In goroutine - it should handle the publish error gracefully
		go handler.In()

		// Wait for error to be processed
		time.Sleep(10 * time.Millisecond)

		// This should not panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Unexpected panic in In method with publish errors: %v", r)
			}
		}()

		// Close the handler
		handler.Close()
	})

	t.Run("Out method with write errors", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create a connection that will return an error on write
		conn := &mockWebSocketConnection{
			writeMessageFunc: func(messageType int, data []byte) error {
				return errors.New("write error")
			},
		}

		handler := &dataStreamHandler{
			ctx:   ctx,
			ctxC:  cancel,
			conn:  conn,
			ch:    make(chan []byte, 1),
			errCh: make(chan error, 1),
		}

		// Send some data to trigger a write
		handler.ch <- []byte("test data")

		// Start Out goroutine - it should handle the write error gracefully
		go handler.Out()

		// Wait for error to be processed
		time.Sleep(10 * time.Millisecond)

		// This should not panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Unexpected panic in Out method with write errors: %v", r)
			}
		}()

		// Close the handler
		handler.Close()
	})

	t.Run("rapid close and restart", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		conn := &mockWebSocketConnection{
			readMessageFunc: func() (int, []byte, error) {
				time.Sleep(5 * time.Millisecond) // Small delay to allow close to happen
				return websocket.TextMessage, []byte("test"), nil
			},
		}

		handler := &dataStreamHandler{
			ctx:     ctx,
			ctxC:    cancel,
			conn:    conn,
			ch:      make(chan []byte, 1),
			errCh:   make(chan error, 1),
			matcher: &mockMatchDefinition{},
			srv: &mockLocalService{
				pubSubPublishFunc: func(ctx context.Context, topic string, data []byte) error {
					return nil
				},
			},
		}

		// Start goroutines
		go handler.In()
		go handler.Out()

		// Close quickly
		handler.Close()

		// Try to start again (this should be safe)
		handler2 := &dataStreamHandler{
			ctx:     ctx,
			ctxC:    cancel,
			conn:    conn,
			ch:      make(chan []byte, 1),
			errCh:   make(chan error, 1),
			matcher: &mockMatchDefinition{},
			srv: &mockLocalService{
				pubSubPublishFunc: func(ctx context.Context, topic string, data []byte) error {
					return nil
				},
			},
		}

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Unexpected panic during rapid close/restart: %v", r)
			}
		}()

		// This should be safe
		handler2.Close()
	})
}
