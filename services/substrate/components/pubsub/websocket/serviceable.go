package websocket

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	iface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
	"github.com/taubyte/tau/utils/id"
)

var _ commonIface.Serviceable = &WebSocket{}
var _ iface.Serviceable = &WebSocket{}

// WebSocket is a Serviceable that gates access to a messaging channel over a
// websocket. It carries no runtime state of its own: the actual bridging
// between the connection and pubsub happens in the handler/datastream layer,
// this only exists so CheckTns/Cache can validate and cache a matching
// configuration the same way function serviceables do.
type WebSocket struct {
	ctx  context.Context
	ctxC context.CancelFunc

	srv iface.Service
	mmi common.MessagingMapItem

	matcher *common.MatchDefinition
	commit  string
	branch  string

	closeOnce sync.Once
}

func New(srv iface.Service, mmi common.MessagingMapItem, commit, branch string, matcher *common.MatchDefinition) (commonIface.Serviceable, error) {
	if matcher == nil {
		return nil, fmt.Errorf("matcher is nil")
	}

	ws := &WebSocket{
		srv:     srv,
		mmi:     mmi,
		matcher: matcher,
		commit:  commit,
		branch:  branch,
	}

	ws.ctx, ws.ctxC = context.WithCancel(srv.Context())

	if _, err := srv.Cache().Add(ws); err != nil {
		return nil, fmt.Errorf("adding pubsub websocket serviceable failed with: %s", err)
	}

	if err := ws.Validate(matcher); err != nil {
		return nil, fmt.Errorf("validating websocket with id `%s` failed with: %s", ws.Id(), err)
	}

	return ws, nil
}

// Id is deterministic on project/channel rather than the dead code's
// hardcoded "". The runtime cache keys entries by
// cacheMap[matcher.CachePrefix()][serviceable.Id()], and CachePrefix is just
// the project - a constant Id would collide across every websocket channel
// in the same project. Deterministic (vs random) so repeated CheckTns calls
// for the same channel converge on one cache entry instead of piling up
// duplicates.
func (ws *WebSocket) Id() string {
	return id.GenerateDeterministic(ws.matcher.Project, ws.matcher.Channel, "websocket")
}

func (ws *WebSocket) Ready() error {
	return nil
}

func (ws *WebSocket) Project() string {
	return ws.matcher.Project
}

func (ws *WebSocket) Application() string {
	return ws.matcher.Application
}

func (ws *WebSocket) Commit() string {
	return ws.commit
}

func (ws *WebSocket) Branch() string {
	return ws.branch
}

func (ws *WebSocket) Matcher() commonIface.MatchDefinition {
	return ws.matcher
}

func (ws *WebSocket) Service() commonIface.ServiceComponent {
	return ws.srv
}

func (ws *WebSocket) Config() *structureSpec.Function {
	return nil
}

func (ws *WebSocket) AssetId() string {
	return ""
}

func (ws *WebSocket) Name() string {
	return strings.Join(ws.mmi.Names(), ",")
}

// HandleMessage is a no-op: a websocket serviceable is a config/authorization
// gate for CheckTns/Cache lookups, not a message consumer - the connection
// itself is bridged to pubsub directly by the handler/datastream layer.
func (ws *WebSocket) HandleMessage(msg iface.Message) (time.Time, error) {
	return time.Now(), nil
}

func (ws *WebSocket) Match(matcher commonIface.MatchDefinition) matcherSpec.Index {
	_matcher, ok := matcher.(*common.MatchDefinition)
	if !ok {
		return matcherSpec.NoMatch
	}

	if len(ws.mmi.Matches(_matcher.Channel)) > 0 {
		return matcherSpec.HighMatch
	}

	return matcherSpec.NoMatch
}

func (ws *WebSocket) Validate(matcher commonIface.MatchDefinition) error {
	if len(ws.mmi.Matches(ws.matcher.Channel)) == 0 {
		return errors.New("websocket channels do not match")
	}

	return nil
}

func (ws *WebSocket) Close() {
	ws.closeOnce.Do(func() {
		ws.ctxC()
	})
}
