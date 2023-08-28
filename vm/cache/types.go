package cache

import (
	"sync"

	iface "github.com/taubyte/go-interfaces/services/substrate/components"
)

// The Cache struct wraps cache methods for use by node-services.
type Cache struct {
	cacheMap map[string]map[string]cacheItem
	locker   sync.RWMutex
}

type cacheItem struct {
	serviceable iface.Serviceable
	assetCid    string
}
