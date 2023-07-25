package tns

import (
	"context"
	"sync"

	"github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/p2p/peer"
	client "github.com/taubyte/p2p/streams/client"
)

type Client struct {
	node   peer.Node
	client *client.Client
	cache  *cache
}

type subscription struct {
	ctx  context.Context
	ctxC context.CancelFunc

	virtualCtx  context.Context
	virtualCtxC context.CancelFunc
}

type cache struct {
	node          peer.Node
	lock          sync.RWMutex
	data          map[string]interface{}
	subscriptions map[string]*subscription
}

type responseObject struct {
	object interface{}
	path   tns.Path
	tns    *Client
}
