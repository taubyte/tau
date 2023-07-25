package cache

import "context"

func (c *cache) watch(ctx context.Context, item *cacheItem) {
	<-ctx.Done()
	c.RLock()
	item.count.Add(-1)
	c.RUnlock()
}
