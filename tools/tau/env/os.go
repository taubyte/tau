package env

import (
	"os"
	"sync"
)

// cache is a wrapper for os.LookupEnv to cache requested values
type osEnvCache struct {
	sync.Mutex
	values map[string]string
}

var _cache *osEnvCache

func init() {
	_cache = &osEnvCache{
		values: make(map[string]string),
	}
}

func Clear() {
	_cache = &osEnvCache{
		values: make(map[string]string),
	}
}

func LookupEnv(key string) (string, bool) {
	_cache.Lock()
	defer _cache.Unlock()

	value, exist := _cache.values[key]
	if exist {
		return value, true
	}

	value, exist = os.LookupEnv(key)
	if exist {
		_cache.values[key] = value
	}

	return value, exist
}
