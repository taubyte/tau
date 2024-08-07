package websocket

import (
	"context"
	"io"
	"sync"

	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	p2p "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
)

var _ commonIface.Serviceable = &WebSocket{}

type WebSocket struct {
	ctx       context.Context
	ctxC      context.CancelFunc
	srv       common.LocalService
	dagReader io.ReadSeekCloser
	project   string
	mmi       common.MessagingMapItem
	matcher   *common.MatchDefinition

	commit string
	branch string
}

func (w *WebSocket) Close() {
	w.ctxC()
}

type WrappedMessage struct {
	Message []byte `json:"message"`
	Error   string `json:"error"`
}

type sub struct {
	handler     p2p.PubSubConsumerHandler
	err_handler p2p.PubSubConsumerErrorHandler
}

type subViewer struct {
	subs   map[int]*sub
	nextId int
	sync.Mutex
}

type subsViewer struct {
	subscriptions map[string]*subViewer
	sync.Mutex
}
