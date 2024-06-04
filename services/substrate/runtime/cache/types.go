package cache

import (
	"sync"

	"github.com/taubyte/tau/core/services/substrate/components"
)

// The Cache struct wraps cache methods for use by node-services.
type Cache struct {
	cacheMap map[string]map[string]components.Serviceable
	locker   sync.RWMutex
}
