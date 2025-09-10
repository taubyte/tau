package websocket

import (
	"context"
	"errors"
	"testing"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
	"gotest.tools/v3/assert"
)

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

		assert.NilError(t, err)
		assert.Assert(t, result != nil, "Expected result to not be nil")

		// Verify the returned WebSocket has correct values
		ws, ok := result.(*WebSocket)
		if !ok {
			t.Error("Expected result to be *WebSocket")
			return
		}

		assert.Equal(t, ws.srv, srv)

		// Check that mmi was set (we can't easily compare structs with slices)
		assert.Equal(t, ws.mmi.Len(), mmi.Len())

		assert.Equal(t, ws.matcher, matcher)

		assert.Equal(t, ws.commit, commit)

		assert.Equal(t, ws.branch, branch)

		assert.Equal(t, ws.project, matcher.Project)

		assert.Assert(t, ws.ctx != nil, "Expected ctx to be set")

		assert.Assert(t, ws.ctxC != nil, "Expected ctxC to be set")
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

		assert.Equal(t, err, attachError)

		assert.Assert(t, result == nil, "Expected result to be nil when AttachWebSocket fails")
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

		assert.Assert(t, err != nil, "Expected error for nil matcher")

		assert.Assert(t, result == nil, "Expected result to be nil when matcher is nil")

		assert.Error(t, err, "matcher is nil")
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

		assert.NilError(t, err)
		assert.Assert(t, result != nil, "Expected result to not be nil")

		// Verify the context is cancelled
		ws, ok := result.(*WebSocket)
		assert.Assert(t, ok, "Expected result to be *WebSocket")

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
	assert.Equal(t, id, "")
}

func TestWebSocket_Ready(t *testing.T) {
	ws := &WebSocket{}

	err := ws.Ready()

	// The Ready method should return nil
	assert.NilError(t, err)
}
