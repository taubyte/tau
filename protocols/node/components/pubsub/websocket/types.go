package websocket

import (
	"context"
	"io"
	"sync"

	p2p "bitbucket.org/taubyte/p2p/peer"
	commonIface "github.com/taubyte/go-interfaces/services/substrate/common"
	"github.com/taubyte/odo/protocols/node/components/pubsub/common"
)

var _ commonIface.Serviceable = &WebSocket{}

type WebSocket struct {
	ctx       context.Context
	ctxC      context.CancelFunc
	srv       common.LocalService
	dagReader io.ReadSeekCloser
	project   string
	mmi       common.MessagingMapItem
	verbose   bool
	matcher   *common.MatchDefinition
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
