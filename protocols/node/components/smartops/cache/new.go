package cache

import (
	"context"
	"time"

	"github.com/taubyte/go-interfaces/services/substrate"
)

func New(ctx context.Context) substrate.SmartOpsCache {
	c := &cache{
		items: make(map[string]*cacheItem),
	}
	c.garbageCtx, c.garbageCtxC = context.WithCancel(ctx)

	go func() {
		for {
			select {
			case <-time.After(cacheItemTTL):
				c.garbageCollect()
			case <-c.garbageCtx.Done():
				return
			}
		}
	}()

	return c
}
