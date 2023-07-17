package cache

import (
	"context"
	"time"
)

func New(ctx context.Context) *cache {
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
