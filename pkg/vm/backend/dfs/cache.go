package dfs

import (
	"container/list"
	"sync"
)

// moduleCache is a byte-size-capped LRU of decompressed wasm module bytes,
// keyed by CID. Content behind a CID is immutable, so entries never go stale.
type moduleCache struct {
	lock     sync.Mutex
	items    map[string]*list.Element
	order    *list.List
	size     uint64
	capacity uint64
}

type cacheEntry struct {
	cid  string
	data []byte
}

func newModuleCache(capacity uint64) *moduleCache {
	return &moduleCache{
		items:    make(map[string]*list.Element),
		order:    list.New(),
		capacity: capacity,
	}
}

func (c *moduleCache) get(cid string) ([]byte, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	elem, ok := c.items[cid]
	if !ok {
		return nil, false
	}

	c.order.MoveToFront(elem)
	return elem.Value.(*cacheEntry).data, true
}

func (c *moduleCache) put(cid string, data []byte) {
	size := uint64(len(data))
	if size > c.capacity {
		// Blob alone can never fit; skip caching it rather than evicting everything else.
		return
	}

	c.lock.Lock()
	defer c.lock.Unlock()

	if elem, ok := c.items[cid]; ok {
		entry := elem.Value.(*cacheEntry)
		c.size += size - uint64(len(entry.data))
		entry.data = data
		c.order.MoveToFront(elem)
	} else {
		c.items[cid] = c.order.PushFront(&cacheEntry{cid: cid, data: data})
		c.size += size
	}

	for c.size > c.capacity {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}

		entry := oldest.Value.(*cacheEntry)
		c.order.Remove(oldest)
		delete(c.items, entry.cid)
		c.size -= uint64(len(entry.data))
	}
}
