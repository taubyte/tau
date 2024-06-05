package tns

import (
	"context"
	"sync"
	"time"

	"github.com/taubyte/p2p/peer"
	client "github.com/taubyte/p2p/streams/client"
	"github.com/taubyte/tau/core/services/tns"
)

type Client struct {
	node   peer.Node
	client *client.Client
	cache  *cache
}

type Stats Client

type subscription struct {
	ctx      context.Context
	ctxC     context.CancelFunc
	cache    *cache
	topic    string
	key      chan string
	keys     []string
	deadline time.Time
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
