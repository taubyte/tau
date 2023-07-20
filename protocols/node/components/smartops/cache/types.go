package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/taubyte/go-interfaces/services/substrate"
)

var cacheItemTTL = 300 * time.Second

type cache struct {
	sync.RWMutex

	garbageCtx  context.Context
	garbageCtxC context.CancelFunc

	// Keeps track of how many serviceables are alive using a given smartOp
	items map[string]*cacheItem
}

type cacheItem struct {
	instance substrate.Instance
	count    atomic.Int32
}

func (c *cache) Close() {
	c.Lock()
	c.items = nil
	c.Unlock()

	c.garbageCtxC()
}
