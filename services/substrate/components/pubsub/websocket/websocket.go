package websocket

import (
	"errors"
	"strings"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/taubyte/tau/core/services/substrate/components"
	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
)

func (ws *WebSocket) Project() string {
	return ws.project
}

func (ws *WebSocket) Application() string {
	return ws.matcher.Application
}

func (ws *WebSocket) HandleMessage(msg *pubsub.Message) (time.Time, error) {
	return time.Now(), nil
}

func (ws *WebSocket) Match(matcher components.MatchDefinition) (currentMatchIndex matcherSpec.Index) {
	currentMatch := matcherSpec.NoMatch
	_matcher, ok := matcher.(*common.MatchDefinition)
	if !ok {
		return
	}

	if len(ws.mmi.Matches(_matcher.Channel)) > 0 {
		currentMatch = matcherSpec.HighMatch
	}

	return currentMatch
}

func (ws *WebSocket) Commit() string {
	return ws.commit
}

func (ws *WebSocket) Branch() string {
	return ws.branch
}

func (ws *WebSocket) Validate(matcher components.MatchDefinition) error {
	if len(ws.mmi.Matches(ws.matcher.Channel)) == 0 {
		return errors.New("websocket channels do not match")
	}

	return nil
}

func (ws *WebSocket) Matcher() components.MatchDefinition {
	return ws.matcher
}

func (ws *WebSocket) Clean() {
	ws.ctxC()
	if ws.dagReader != nil {
		ws.dagReader.Close()
	}
}

func (ws *WebSocket) Name() string {
	return strings.Join(ws.mmi.Names(), ",")
}

var AttachWebSocket = func(ws *WebSocket) error {
	// For tests to be overridden when attaching a websocket
	return nil
}

func (ws *WebSocket) Service() components.ServiceComponent {
	return ws.srv
}

// TODO: Fix this
func (ws *WebSocket) Config() *structureSpec.Function {
	return nil
}

func (ws *WebSocket) AssetId() string {
	return ""
}

func (w *WebSocket) Close() {
	w.ctxC()
}
