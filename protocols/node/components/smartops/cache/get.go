package cache

import (
	"context"

	iface "github.com/taubyte/go-interfaces/services/substrate/smartops"
	"github.com/taubyte/utils/multihash"
)

func (c *cache) Get(project, application, smartOpId string, ctx context.Context) (instance iface.Instance, ok bool) {
	hash := multihash.Hash(project + application + smartOpId)

	c.RLock()
	defer c.RUnlock()

	item, ok := c.items[hash]
	if !ok {
		return
	}

	item.count.Add(1)

	go c.watch(ctx, item)

	return item.instance, ok
}
