package statetree

import (
	"sync"
	"unsafe"

	cid "github.com/ipfs/go-cid"
)

// A Cache keeps decoded upper-level nodes within a byte budget. That a node is
// cached says nothing of whether a store holds its block.
//
// Cached nodes own their bytes. When full, a node takes the place of the least
// recently used node of the lowest level held, unless that level is above its
// own. The index holds no pointers.
type Cache struct {
	mu     sync.Mutex
	budget int
	used   int
	// pressed counts the nodes that arrived to a full budget.
	pressed int
	index   map[cacheKey]int32
	slots   []cacheSlot
	free    []int32
	// Per level, the first and last slot, most recently used first; -1 for none.
	mru, lru [cacheLevels]int32
}

// A node above the last level shares its list.
const cacheLevels = 32

// Every node's CID: CIDv1, raw, sha2-256.
const cidLen = 4 + 32

type cacheKey [cidLen]byte

type cacheSlot struct {
	n          *node
	key        cacheKey
	size       int32
	level      uint8
	prev, next int32
}

func NewCache(budget int) *Cache {
	c := &Cache{budget: budget, index: map[cacheKey]int32{}}
	for i := range c.mru {
		c.mru[i], c.lru[i] = -1, -1
	}
	return c
}

func WithCache(c *Cache) Option {
	return func(t *Tree) { t.cache = c }
}

func (c *Cache) Used() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

// Pressed is how many nodes arrived when the budget was full, each taking another's
// place or turned away.
func (c *Cache) Pressed() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pressed
}

func keyFor(id cid.Cid) (cacheKey, bool) {
	var k cacheKey
	ks := id.KeyString()
	if len(ks) != cidLen {
		return k, false
	}
	copy(k[:], ks)
	return k, true
}

func levelList(level uint32) uint8 { return uint8(min(level, cacheLevels-1)) }

func (c *Cache) get(id cid.Cid) *node {
	k, ok := keyFor(id)
	if !ok {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	i, ok := c.index[k]
	if !ok {
		return nil
	}
	c.unlink(i)
	c.push(i)
	return c.slots[i].n
}

func (c *Cache) wants(level uint32, size int, root bool) bool {
	if level == 0 && !root || size > c.budget/8 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used+size <= c.budget || c.lowest() <= levelList(level)
}

// lowest is cacheLevels when the cache holds nothing.
func (c *Cache) lowest() uint8 {
	for l := range c.lru {
		if c.lru[l] >= 0 {
			return uint8(l)
		}
	}
	return cacheLevels
}

// add requires n to own its bytes.
func (c *Cache) add(id cid.Cid, n *node, size int) {
	k, ok := keyFor(id)
	if !ok {
		return
	}
	if size > c.budget/8 {
		return
	}
	level := levelList(n.level)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.index[k]; ok {
		return
	}
	if c.used+size > c.budget {
		c.pressed++
	}
	for c.used+size > c.budget {
		l := c.lowest()
		if l > level {
			return
		}
		c.evict(c.lru[l])
	}
	var i int32
	if f := len(c.free); f > 0 {
		i = c.free[f-1]
		c.free = c.free[:f-1]
	} else {
		i = int32(len(c.slots))
		c.slots = append(c.slots, cacheSlot{})
	}
	c.slots[i] = cacheSlot{n: n, key: k, size: int32(size), level: level}
	c.push(i)
	c.index[k] = i
	c.used += size
}

// Forget drops the node of the block with this digest, which no tree reaches
// any more: left in place it would outlive nodes still read.
func (c *Cache) Forget(digest [32]byte) {
	var k cacheKey
	copy(k[copy(k[:], BlockPrefix):], digest[:])
	c.mu.Lock()
	defer c.mu.Unlock()
	if i, ok := c.index[k]; ok {
		c.evict(i)
	}
}

func (c *Cache) evict(i int32) {
	s := &c.slots[i]
	c.unlink(i)
	delete(c.index, s.key)
	c.used -= int(s.size)
	*s = cacheSlot{}
	c.free = append(c.free, i)
}

func (c *Cache) push(i int32) {
	s := &c.slots[i]
	l := s.level
	s.prev, s.next = -1, c.mru[l]
	if s.next >= 0 {
		c.slots[s.next].prev = i
	} else {
		c.lru[l] = i
	}
	c.mru[l] = i
}

func (c *Cache) unlink(i int32) {
	s := &c.slots[i]
	l := s.level
	if s.prev >= 0 {
		c.slots[s.prev].next = s.next
	} else {
		c.mru[l] = s.next
	}
	if s.next >= 0 {
		c.slots[s.next].prev = s.prev
	} else {
		c.lru[l] = s.prev
	}
}

const (
	nodeSize  = int(unsafe.Sizeof(node{}))
	entrySize = int(unsafe.Sizeof(entry{}))
	refSize   = int(unsafe.Sizeof(ref{}))
	slotSize  = int(unsafe.Sizeof(cacheSlot{})) + cidLen + 4
)

func footprint(entries, buf, cids int) int {
	return slotSize + nodeSize + entries*entrySize + (entries+1)*refSize + buf + cids
}
