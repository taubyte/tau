package websocket

import (
	"context"
	"io"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
	"gotest.tools/v3/assert"
)

// Mock implementations for testing
type mockReadSeekCloser struct {
	closeFunc func() error
}

func (m *mockReadSeekCloser) Read(p []byte) (n int, err error) {
	return 0, io.EOF
}

func (m *mockReadSeekCloser) Seek(offset int64, whence int) (int64, error) {
	return 0, nil
}

func (m *mockReadSeekCloser) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

func TestWebSocket_Project(t *testing.T) {
	ws := &WebSocket{
		project: "test-project",
	}

	result := ws.Project()

	if result != "test-project" {
		t.Errorf("Expected 'test-project', got '%s'", result)
	}
}

func TestWebSocket_Application(t *testing.T) {
	matcher := &common.MatchDefinition{
		Application: "test-app",
	}

	ws := &WebSocket{
		matcher: matcher,
	}

	result := ws.Application()

	if result != "test-app" {
		t.Errorf("Expected 'test-app', got '%s'", result)
	}
}

func TestWebSocket_HandleMessage(t *testing.T) {
	ws := &WebSocket{}

	msg := &pubsub.Message{}

	startTime := time.Now()
	timestamp, err := ws.HandleMessage(msg)
	endTime := time.Now()

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Check that timestamp is between start and end time
	if timestamp.Before(startTime) || timestamp.After(endTime) {
		t.Errorf("Expected timestamp to be between %v and %v, got %v", startTime, endTime, timestamp)
	}
}

func TestWebSocket_Match(t *testing.T) {
	t.Run("successful match", func(t *testing.T) {
		mmi := common.MessagingMapItem{}
		mmi.Push("test-project", "test-app", &structureSpec.Messaging{
			Name:  "test-name",
			Match: "test-channel",
		})

		matcher := &common.MatchDefinition{
			Channel: "test-channel",
		}

		ws := &WebSocket{
			mmi:     mmi,
			matcher: matcher,
		}

		result := ws.Match(matcher)

		if result != matcherSpec.HighMatch {
			t.Errorf("Expected HighMatch, got %v", result)
		}
	})

	t.Run("no match", func(t *testing.T) {
		mmi := common.MessagingMapItem{}
		mmi.Push("test-project", "test-app", &structureSpec.Messaging{
			Name:  "test-name",
			Match: "different-channel",
		})

		matcher := &common.MatchDefinition{
			Channel: "test-channel",
		}

		ws := &WebSocket{
			mmi:     mmi,
			matcher: matcher,
		}

		result := ws.Match(matcher)

		if result != matcherSpec.NoMatch {
			t.Errorf("Expected NoMatch, got %v", result)
		}
	})

	t.Run("invalid matcher type", func(t *testing.T) {
		ws := &WebSocket{}

		// Pass a different type that doesn't implement the interface
		result := ws.Match(nil)

		if result != 0 { // matcherSpec.NoMatch is 0
			t.Errorf("Expected NoMatch (0) for invalid matcher type, got %v", result)
		}
	})
}

func TestWebSocket_Commit(t *testing.T) {
	ws := &WebSocket{
		commit: "test-commit",
	}

	result := ws.Commit()

	if result != "test-commit" {
		t.Errorf("Expected 'test-commit', got '%s'", result)
	}
}

func TestWebSocket_Branch(t *testing.T) {
	ws := &WebSocket{
		branch: "test-branch",
	}

	result := ws.Branch()

	if result != "test-branch" {
		t.Errorf("Expected 'test-branch', got '%s'", result)
	}
}

func TestWebSocket_Validate(t *testing.T) {
	t.Run("valid match", func(t *testing.T) {
		mmi := common.MessagingMapItem{}
		mmi.Push("test-project", "test-app", &structureSpec.Messaging{
			Name:  "test-name",
			Match: "test-channel",
		})

		matcher := &common.MatchDefinition{
			Channel: "test-channel",
		}

		ws := &WebSocket{
			mmi:     mmi,
			matcher: matcher,
		}

		err := ws.Validate(matcher)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
	})

	t.Run("no match", func(t *testing.T) {
		mmi := common.MessagingMapItem{}
		mmi.Push("test-project", "test-app", &structureSpec.Messaging{
			Name:  "test-name",
			Match: "different-channel",
		})

		matcher := &common.MatchDefinition{
			Channel: "test-channel",
		}

		ws := &WebSocket{
			mmi:     mmi,
			matcher: matcher,
		}

		err := ws.Validate(matcher)

		if err == nil {
			t.Error("Expected error for no match")
		}

		expectedErr := "websocket channels do not match"
		if err.Error() != expectedErr {
			t.Errorf("Expected error '%s', got '%s'", expectedErr, err.Error())
		}
	})
}

func TestWebSocket_Matcher(t *testing.T) {
	matcher := &common.MatchDefinition{
		Channel: "test-channel",
	}

	ws := &WebSocket{
		matcher: matcher,
	}

	result := ws.Matcher()

	if result != matcher {
		t.Error("Expected matcher to be returned")
	}
}

func TestWebSocket_Clean(t *testing.T) {
	t.Run("with dagReader", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		closeCalled := false
		mockReader := &mockReadSeekCloser{
			closeFunc: func() error {
				closeCalled = true
				return nil
			},
		}

		ws := &WebSocket{
			ctx:       ctx,
			ctxC:      cancel,
			dagReader: mockReader,
		}

		ws.Clean()

		if !closeCalled {
			t.Error("Expected dagReader.Close() to be called")
		}
	})

	t.Run("without dagReader", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		ws := &WebSocket{
			ctx:  ctx,
			ctxC: cancel,
		}

		// This should not panic
		ws.Clean()
	})
}

func TestWebSocket_Name(t *testing.T) {
	t.Run("with names", func(t *testing.T) {
		mmi := common.MessagingMapItem{}
		mmi.Push("test-project", "test-app", &structureSpec.Messaging{
			Name:  "test-name-1",
			Match: "test-channel-1",
		})
		mmi.Push("test-project", "test-app", &structureSpec.Messaging{
			Name:  "test-name-2",
			Match: "test-channel-2",
		})

		ws := &WebSocket{
			mmi: mmi,
		}

		result := ws.Name()

		expected := "test-name-1,test-name-2"
		if result != expected {
			t.Errorf("Expected '%s', got '%s'", expected, result)
		}
	})

	t.Run("single name", func(t *testing.T) {
		mmi := common.MessagingMapItem{}
		mmi.Push("test-project", "test-app", &structureSpec.Messaging{
			Name:  "single-name",
			Match: "test-channel",
		})

		ws := &WebSocket{
			mmi: mmi,
		}

		result := ws.Name()

		expected := "single-name"
		if result != expected {
			t.Errorf("Expected '%s', got '%s'", expected, result)
		}
	})
}

func TestWebSocket_Service(t *testing.T) {
	srv := &mockLocalService{}

	ws := &WebSocket{
		srv: srv,
	}

	result := ws.Service()

	if result != srv {
		t.Error("Expected service to be returned")
	}
}

func TestWebSocket_Config(t *testing.T) {
	ws := &WebSocket{}

	result := ws.Config()

	if result != nil {
		t.Error("Expected Config to return nil")
	}
}

func TestWebSocket_AssetId(t *testing.T) {
	ws := &WebSocket{}

	result := ws.AssetId()

	if result != "" {
		t.Errorf("Expected empty string, got '%s'", result)
	}
}

func TestAttachWebSocket(t *testing.T) {
	ws := &WebSocket{}

	err := AttachWebSocket(ws)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}

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
