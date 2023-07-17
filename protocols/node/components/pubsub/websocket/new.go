package websocket

import (
	"context"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/common"
	"github.com/taubyte/go-interfaces/services/substrate/counters"
	"github.com/taubyte/odo/protocols/node/components/pubsub/common"
)

func New(srv common.LocalService, mmi common.MessagingMapItem, matcher *common.MatchDefinition) (commonIface.Serviceable, error) {
	ctx, ctxC := context.WithCancel(srv.Context())
	ws := &WebSocket{
		ctx:     ctx,
		ctxC:    ctxC,
		srv:     srv,
		mmi:     mmi,
		verbose: srv.Verbose(),
		matcher: matcher,
	}
	ws.project = matcher.Project

	err := AttachWebSocket(ws)
	if err != nil {
		return nil, err
	}

	return ws, nil
}

func (w *WebSocket) Id() (id string) {
	return
}

func (w *WebSocket) Ready() error {
	return nil
}

func (w *WebSocket) Counter() counters.Service {
	return w.srv.Counter()
}
