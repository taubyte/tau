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
