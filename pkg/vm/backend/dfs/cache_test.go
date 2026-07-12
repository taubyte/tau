package dfs

import (
	"strconv"
	"sync"
	"testing"

	"gotest.tools/v3/assert"
)

func TestModuleCacheMiss(t *testing.T) {
	c := newModuleCache(1024)

	_, ok := c.get("missing")
	assert.Equal(t, ok, false)
}

func TestModuleCacheHit(t *testing.T) {
	c := newModuleCache(1024)

	c.put("a", []byte("hello"))

	data, ok := c.get("a")
	assert.Equal(t, ok, true)
	assert.DeepEqual(t, data, []byte("hello"))
}

func TestModuleCacheRecencyBump(t *testing.T) {
	c := newModuleCache(15)

	c.put("a", []byte("aaaaa")) // 5 bytes
	c.put("b", []byte("bbbbb")) // 5 bytes
	c.put("c", []byte("ccccc")) // 5 bytes, total 15 == cap

	// touch "a" so it becomes the most recently used entry
	_, ok := c.get("a")
	assert.Equal(t, ok, true)

	// pushes total size to 20, forcing an eviction; "b" is now the oldest
	c.put("d", []byte("ddddd"))

	_, ok = c.get("b")
	assert.Equal(t, ok, false)

	_, ok = c.get("a")
	assert.Equal(t, ok, true)

	_, ok = c.get("c")
	assert.Equal(t, ok, true)

	_, ok = c.get("d")
	assert.Equal(t, ok, true)
}

func TestModuleCacheEvictsOldestFirstAndTracksSize(t *testing.T) {
	c := newModuleCache(10)

	c.put("a", []byte("aaaaa")) // 5 bytes
	c.put("b", []byte("bbbbb")) // 5 bytes, total 10 == cap
	assert.Equal(t, c.size, uint64(10))

	// forces eviction of "a", the oldest entry
	c.put("c", []byte("ccccc"))

	_, ok := c.get("a")
	assert.Equal(t, ok, false)

	_, ok = c.get("b")
	assert.Equal(t, ok, true)

	_, ok = c.get("c")
	assert.Equal(t, ok, true)

	assert.Equal(t, c.size, uint64(10))
}

func TestModuleCacheOverwriteTracksSize(t *testing.T) {
	c := newModuleCache(1024)

	c.put("a", []byte("aaaaaaaaaa")) // 10 bytes
	assert.Equal(t, c.size, uint64(10))

	// grow the same key
	c.put("a", []byte("bbbbbbbbbbbbbbb")) // 15 bytes
	assert.Equal(t, c.size, uint64(15))

	// shrink the same key (exercises the uint64 subtraction path)
	c.put("a", []byte("cc")) // 2 bytes
	assert.Equal(t, c.size, uint64(2))

	data, ok := c.get("a")
	assert.Equal(t, ok, true)
	assert.DeepEqual(t, data, []byte("cc"))
}

func TestModuleCacheConcurrentAccess(t *testing.T) {
	c := newModuleCache(64 << 10)

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := range 200 {
				key := strconv.Itoa((i + j) % 8)
				c.put(key, []byte(key))
				c.get(key)
			}
		}(i)
	}
	wg.Wait()

	assert.Assert(t, c.size <= c.capacity)
}

func TestModuleCacheOversizeBlobNotCached(t *testing.T) {
	c := newModuleCache(4)

	c.put("big", []byte("hello")) // 5 bytes > 4 byte cap

	_, ok := c.get("big")
	assert.Equal(t, ok, false)
	assert.Equal(t, c.size, uint64(0))
}
