package tns

import (
	"context"
	"sync"
	"time"

	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/peer"
	client "github.com/taubyte/tau/p2p/streams/client"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
)

type Client struct {
	node   peer.Node
	client *client.Client
	peers  []peerCore.ID
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
