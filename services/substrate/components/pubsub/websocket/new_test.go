package websocket

import (
	"context"
	"errors"
	"testing"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
)

// We'll use the actual MessagingMapItem with made-up values

func TestNew(t *testing.T) {
	t.Run("successful creation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		srv := &mockLocalService{
			contextFunc: func() context.Context {
				return ctx
			},
		}

		// Create a MessagingMapItem with test data
		mmi := common.MessagingMapItem{}
		mmi.Push("test-project", "test-app", &structureSpec.Messaging{
			Name:  "test-name",
			Match: "test-channel",
		})

		matcher := &common.MatchDefinition{
			Channel:     "test-channel",
			Project:     "test-project",
			Application: "test-app",
			WebSocket:   true,
		}

		commit := "test-commit"
		branch := "test-branch"

		// Override AttachWebSocket for testing
		originalAttachWebSocket := AttachWebSocket
		AttachWebSocket = func(ws *WebSocket) error {
			return nil
		}
		defer func() {
			AttachWebSocket = originalAttachWebSocket
		}()

		result, err := New(srv, mmi, commit, branch, matcher)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}

		if result == nil {
			t.Error("Expected result to not be nil")
		}

		// Verify the returned WebSocket has correct values
		ws, ok := result.(*WebSocket)
		if !ok {
			t.Error("Expected result to be *WebSocket")
			return
		}

		if ws.srv != srv {
			t.Error("Expected srv to be set correctly")
		}

		// Check that mmi was set (we can't easily compare structs with slices)
		if ws.mmi.Len() != mmi.Len() {
			t.Errorf("Expected mmi length %d, got %d", mmi.Len(), ws.mmi.Len())
		}

		if ws.matcher != matcher {
			t.Error("Expected matcher to be set correctly")
		}

		if ws.commit != commit {
			t.Errorf("Expected commit to be %s, got %s", commit, ws.commit)
		}

		if ws.branch != branch {
			t.Errorf("Expected branch to be %s, got %s", branch, ws.branch)
		}

		if ws.project != matcher.Project {
			t.Errorf("Expected project to be %s, got %s", matcher.Project, ws.project)
		}

		if ws.ctx == nil {
			t.Error("Expected ctx to be set")
		}

		if ws.ctxC == nil {
			t.Error("Expected ctxC to be set")
		}
	})

	t.Run("AttachWebSocket error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		srv := &mockLocalService{
			contextFunc: func() context.Context {
				return ctx
			},
		}

		mmi := common.MessagingMapItem{}
		matcher := &common.MatchDefinition{
			Channel:     "test-channel",
			Project:     "test-project",
			Application: "test-app",
			WebSocket:   true,
		}

		attachError := errors.New("attach error")

		// Override AttachWebSocket to return an error
		originalAttachWebSocket := AttachWebSocket
		AttachWebSocket = func(ws *WebSocket) error {
			return attachError
		}
		defer func() {
			AttachWebSocket = originalAttachWebSocket
		}()

		result, err := New(srv, mmi, "commit", "branch", matcher)

		if err != attachError {
			t.Errorf("Expected error %v, got %v", attachError, err)
		}

		if result != nil {
			t.Error("Expected result to be nil when AttachWebSocket fails")
		}
	})

	t.Run("nil matcher", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		srv := &mockLocalService{
			contextFunc: func() context.Context {
				return ctx
			},
		}

		mmi := common.MessagingMapItem{}

		result, err := New(srv, mmi, "commit", "branch", nil)

		if err == nil {
			t.Error("Expected error for nil matcher")
		}

		if result != nil {
			t.Error("Expected result to be nil when matcher is nil")
		}

		expectedErr := "matcher is nil"
		if err.Error() != expectedErr {
			t.Errorf("Expected error '%s', got '%s'", expectedErr, err.Error())
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		srv := &mockLocalService{
			contextFunc: func() context.Context {
				return ctx
			},
		}

		mmi := common.MessagingMapItem{}
		matcher := &common.MatchDefinition{
			Channel:     "test-channel",
			Project:     "test-project",
			Application: "test-app",
			WebSocket:   true,
		}

		// Override AttachWebSocket for testing
		originalAttachWebSocket := AttachWebSocket
		AttachWebSocket = func(ws *WebSocket) error {
			return nil
		}
		defer func() {
			AttachWebSocket = originalAttachWebSocket
		}()

		result, err := New(srv, mmi, "commit", "branch", matcher)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}

		if result == nil {
			t.Error("Expected result to not be nil")
		}

		// Verify the context is cancelled
		ws, ok := result.(*WebSocket)
		if !ok {
			t.Error("Expected result to be *WebSocket")
			return
		}

		select {
		case <-ws.ctx.Done():
			// Expected - context should be cancelled
		default:
			t.Error("Expected context to be cancelled")
		}
	})
}

func TestWebSocket_Id(t *testing.T) {
	ws := &WebSocket{}

	id := ws.Id()

	// The Id method returns an empty string by default
	if id != "" {
		t.Errorf("Expected empty string, got %s", id)
	}
}

func TestWebSocket_Ready(t *testing.T) {
	ws := &WebSocket{}

	err := ws.Ready()

	// The Ready method should return nil
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}
