package statetree

import (
	"sync"
	"unsafe"

	cid "github.com/ipfs/go-cid"
)

// A Cache keeps decoded nodes of the upper levels of trees, the ones every
// change and every diff of a tree passes through, from one tree to the next,
// within a budget of bytes. A node is named by its CID, so a cached node is
// that node whatever tree reads it. A Tree given a cache asks it before its
// DAG service, and only to decode a node: that a node is cached says nothing
// of whether any store still holds its block.
//
// The nodes it keeps are roots and nodes above level 0. A cached node owns
// its bytes, all in one buffer, and aliases no block. Once the budget is
// spent a node takes the place of the least recently used node of the
// lowest level the cache holds, unless that level is above its own.
//
// Its index holds no pointers: a node is found by the bytes of its CID, and
// its slot by number.
type Cache struct {
	mu     sync.Mutex
	budget int
	used   int
	index  map[cacheKey]int32
	slots  []cacheSlot
	free   []int32
	// mru and lru are, for each level, the first and last slot of its list,
	// most recently used first; -1 for none.
	mru, lru [cacheLevels]int32
}

// cacheLevels bounds the levels the cache tells apart; a node above the
// last shares its list.
const cacheLevels = 32

// cidLen is the length of the CIDs the cache keeps: CIDv1, dag-pb,
// sha2-256, the CID of every node this package writes.
const cidLen = 4 + 32

type cacheKey [cidLen]byte

type cacheSlot struct {
	n          *node
	key        cacheKey
	size       int32
	level      uint8
	prev, next int32
}

// NewCache is a cache of at most budget bytes.
func NewCache(budget int) *Cache {
	c := &Cache{budget: budget, index: map[cacheKey]int32{}}
	for i := range c.mru {
		c.mru[i], c.lru[i] = -1, -1
	}
	return c
}

// WithCache has a tree keep the upper levels of what it reads in c.
func WithCache(c *Cache) Option {
	return func(t *Tree) { t.cache = c }
}

// Used is the bytes the cache holds.
func (c *Cache) Used() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
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

// get is the node id names, if the cache holds it.
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

// wants reports whether a node of level and about size bytes would be
// kept, root telling whether it is read as a tree's root.
func (c *Cache) wants(level uint32, size int, root bool) bool {
	if level == 0 && !root || size > c.budget/8 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used+size <= c.budget || c.lowest() <= levelList(level)
}

// lowest is the lowest level the cache holds a node of, cacheLevels for
// none.
func (c *Cache) lowest() uint8 {
	for l := range c.lru {
		if c.lru[l] >= 0 {
			return uint8(l)
		}
	}
	return cacheLevels
}

// add keeps n, which owns its bytes and costs size to keep (footprint), as
// the node id names.
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

func (c *Cache) evict(i int32) {
	s := &c.slots[i]
	c.unlink(i)
	delete(c.index, s.key)
	c.used -= int(s.size)
	*s = cacheSlot{}
	c.free = append(c.free, i)
}

// push makes slot i the most recently used of its level.
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
	// slotSize is a slot and its index entry.
	slotSize = int(unsafe.Sizeof(cacheSlot{})) + cidLen + 4
)

// footprint is what keeping a node costs: the node, room for entries
// entries and their kids, its buffer of buf bytes, its kids' CIDs of cids
// bytes, and its slot.
func footprint(entries, buf, cids int) int {
	return slotSize + nodeSize + entries*entrySize + (entries+1)*refSize + buf + cids
}
