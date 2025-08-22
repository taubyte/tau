package basic

import (
	"sync"
	"time"
)

type item struct {
	value      interface{}
	lastAccess int64
}

type TTLMap struct {
	m map[string]*item
	l sync.Mutex
}

// New creates a new TTLMap with a global TTL in seconds
func New(ln int, maxTTL int) (m *TTLMap) {
	m = &TTLMap{m: make(map[string]*item, ln)}
	go func() {
		for now := range time.Tick(time.Second) {
			m.l.Lock()
			for k, v := range m.m {
				if now.Unix()-v.lastAccess > int64(maxTTL) {
					delete(m.m, k)
				}
			}
			m.l.Unlock()
		}
	}()
	return
}

// Len returns the number of items in the map
func (m *TTLMap) Len() int {
	return len(m.m)
}

// Put adds an item to the map
func (m *TTLMap) Put(k string, v interface{}) {
	m.l.Lock()
	it, ok := m.m[k]
	if !ok {
		it = &item{value: v}
		m.m[k] = it
	}
	it.lastAccess = time.Now().Unix()
	m.l.Unlock()
}

// Get returns an item from the map
func (m *TTLMap) Get(k string) (v interface{}) {
	m.l.Lock()
	if it, ok := m.m[k]; ok {
		v = it.value
		it.lastAccess = time.Now().Unix()
	}
	m.l.Unlock()
	return

}
