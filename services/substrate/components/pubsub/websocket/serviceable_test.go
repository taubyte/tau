package websocket

import (
	"context"
	"testing"

	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
)

// foreignMatcher is a MatchDefinition that is not *common.MatchDefinition, used
// to exercise Match's type-assertion guard.
type foreignMatcher struct{}

func (foreignMatcher) String() string      { return "" }
func (foreignMatcher) CachePrefix() string { return "" }

// newTestWS builds a WebSocket serviceable directly (white-box) so the
// config-gate invariants can be checked without standing up a full Service
// mock. It mirrors what New produces post-cache-add.
func newTestWS(t *testing.T, project, channel string) *WebSocket {
	t.Helper()

	var mmi common.MessagingMapItem
	mmi.Push(project, "", &structureSpec.Messaging{Match: channel, WebSocket: true})

	ws := &WebSocket{
		mmi:     mmi,
		matcher: &common.MatchDefinition{Channel: channel, Project: project},
		commit:  "commit",
		branch:  "master",
	}
	ws.ctx, ws.ctxC = context.WithCancel(context.Background())

	return ws
}

// TestWebSocketCloseIdempotent locks in the sync.Once guard: Close must be safe
// to call repeatedly (cache.Remove can race with connection teardown).
func TestWebSocketCloseIdempotent(t *testing.T) {
	ws := newTestWS(t, "proj", "chan")

	ws.Close()
	ws.Close()
	ws.Close()

	select {
	case <-ws.ctx.Done():
	default:
		t.Fatal("Close did not cancel the serviceable context")
	}
}

// TestWebSocketHandleMessageNoop guards the mixed-pick contract: a websocket
// serviceable can land in a non-websocket Lookup's picks, where subscribe.go's
// handle calls HandleMessage on every pick. It must be an error-free no-op.
func TestWebSocketHandleMessageNoop(t *testing.T) {
	ws := newTestWS(t, "proj", "chan")

	if _, err := ws.HandleMessage(nil); err != nil {
		t.Fatalf("HandleMessage must be a no-op, got error: %v", err)
	}
}

// TestWebSocketMatch covers the channel gate and the non-common matcher guard.
func TestWebSocketMatch(t *testing.T) {
	ws := newTestWS(t, "proj", "chan")

	if got := ws.Match(&common.MatchDefinition{Channel: "chan", Project: "proj"}); got != matcherSpec.HighMatch {
		t.Fatalf("expected HighMatch on matching channel, got %v", got)
	}
	if got := ws.Match(&common.MatchDefinition{Channel: "other", Project: "proj"}); got != matcherSpec.NoMatch {
		t.Fatalf("expected NoMatch on non-matching channel, got %v", got)
	}
	var foreign commonIface.MatchDefinition = foreignMatcher{}
	if got := ws.Match(foreign); got != matcherSpec.NoMatch {
		t.Fatalf("expected NoMatch on foreign matcher type, got %v", got)
	}
}

// TestWebSocketIdDeterministic verifies the cache key converges per
// (project, channel) and separates distinct channels, so repeated CheckTns
// calls reuse one cache entry instead of piling up duplicates.
func TestWebSocketIdDeterministic(t *testing.T) {
	a := newTestWS(t, "proj", "chan")
	b := newTestWS(t, "proj", "chan")
	if a.Id() != b.Id() {
		t.Fatalf("same (project, channel) must produce same Id: %q != %q", a.Id(), b.Id())
	}

	c := newTestWS(t, "proj", "other")
	if a.Id() == c.Id() {
		t.Fatal("different channels must produce different Ids")
	}
	if a.Id() == "" {
		t.Fatal("Id must not be empty")
	}
}

// TestWebSocketConfigGate pins the sentinels the mixed-pick helpers rely on:
// Config()==nil (distinguishes ws from function picks) and AssetId=="" (keeps
// cache.validate's asset check harmless).
func TestWebSocketConfigGate(t *testing.T) {
	ws := newTestWS(t, "proj", "chan")

	if ws.Config() != nil {
		t.Fatal("websocket Config() must be nil")
	}
	if ws.AssetId() != "" {
		t.Fatalf("websocket AssetId() must be empty, got %q", ws.AssetId())
	}
	if err := ws.Ready(); err != nil {
		t.Fatalf("Ready() must be nil, got %v", err)
	}
}
