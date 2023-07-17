package cache

func (c *cache) garbageCollect() {
	c.Lock()
	defer c.Unlock()

	for hash, item := range c.items {
		if item.count.Load() == 0 {
			item.instance.ContextCancel()
			delete(c.items, hash)
		}
	}
}
