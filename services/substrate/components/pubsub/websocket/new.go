package websocket

import (
	"context"

	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
)

func New(srv common.LocalService, mmi common.MessagingMapItem, commit, branch string, matcher *common.MatchDefinition) (commonIface.Serviceable, error) {
	ctx, ctxC := context.WithCancel(srv.Context())
	ws := &WebSocket{
		ctx:     ctx,
		ctxC:    ctxC,
		srv:     srv,
		mmi:     mmi,
		matcher: matcher,
		commit:  commit,
		branch:  branch,
	}

	ws.project = matcher.Project

	err := AttachWebSocket(ws)
	if err != nil {
		return nil, err
	}

	ws.Commit()

	return ws, nil
}

func (w *WebSocket) Id() (id string) {
	return
}

func (w *WebSocket) Ready() error {
	return nil
}
