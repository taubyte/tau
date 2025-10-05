package websocket

import (
	"sync"

	p2p "github.com/taubyte/tau/p2p/peer"
)

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
