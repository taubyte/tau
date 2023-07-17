package cache

import (
	"context"
	"fmt"
	"sync/atomic"

	iface "github.com/taubyte/go-interfaces/services/substrate/smartops"
	"github.com/taubyte/utils/multihash"
)

func (c *cache) Put(project, application, smartOpId string, ctx context.Context, instance iface.Instance) error {
	hash := multihash.Hash(project + application + smartOpId)

	c.Lock()
	defer c.Unlock()

	if item := c.items[hash]; item == nil {

		item = &cacheItem{instance, atomic.Int32{}}
		item.count.Add(1)

		c.items[hash] = item

		go c.watch(ctx, item)
	}

	return fmt.Errorf("instance with hash %s already exists", hash)
}
