package websocket

import (
	"errors"
	"time"

	"github.com/ipfs/go-cid"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	commonIface "github.com/taubyte/go-interfaces/services/substrate/common"
	matcherSpec "github.com/taubyte/go-specs/matcher"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/odo/protocols/node/components/pubsub/common"
)

func (ws *WebSocket) Project() (cid.Cid, error) {
	return cid.Decode(ws.project)
}

func (ws *WebSocket) HandleMessage(msg *pubsub.Message) (t time.Time, err error) {
	t = time.Now()
	// handled by ch in ./handler.go, here to fulfill the struct
	return
}

func (ws *WebSocket) Match(matcher commonIface.MatchDefinition) (currentMatchIndex matcherSpec.Index) {
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
	return ws.matcher.Commit
}

func (ws *WebSocket) Validate(matcher commonIface.MatchDefinition) error {
	if len(ws.mmi.Matches(ws.matcher.Channel)) == 0 {
		return errors.New("websocket channels do not match")
	}

	return nil
}

func (ws *WebSocket) Matcher() commonIface.MatchDefinition {
	return ws.matcher
}

func (ws *WebSocket) Clean() {
	ws.ctxC()
	if ws.dagReader != nil {
		ws.dagReader.Close()
	}
}

func (ws *WebSocket) Name() string {
	var name string
	for _, _name := range ws.mmi.Names() {
		name += _name + ","
	}

	// Remove the trailing comma
	return name[0 : len(name)-1]
}

var AttachWebSocket = func(ws *WebSocket) error {
	// For tests to be overridden when attaching a websocket
	return nil
}

func (ws *WebSocket) Service() commonIface.Service {
	return ws.srv
}

// TODO: Fix this
func (ws *WebSocket) Config() *structureSpec.Function {
	return nil
}
