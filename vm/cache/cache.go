package cache

import (
	"fmt"
	"sync"

	iface "github.com/taubyte/go-interfaces/services/substrate/common"
	"github.com/taubyte/go-interfaces/services/tns"
	matcherSpec "github.com/taubyte/go-specs/matcher"
)

// The Cache struct wraps cache methods for use by node-services.
type Cache struct {
	cacheMap map[string]map[string]iface.Serviceable
	locker   sync.RWMutex
}

// Close safely clears the cache and releases resources.
//
// This method locks the cache, sets the cacheMap to nil, and then
// unlocks the cache to ensure that other goroutines can safely access
// the cache once it has been cleared.
func (c *Cache) Close() {
	c.locker.Lock()
	c.cacheMap = nil
	c.locker.Unlock()
}

// New creates new cache object to be used by Node sub-services for caching serviceables.
//
// The serviceables are stored in a map of serviceables map, where the map key is the matcher cache prefix value.
// The serviceable itself is stored by the id of the serviceable.
func New() *Cache {
	return &Cache{
		cacheMap: make(map[string]map[string]iface.Serviceable, 0),
	}
}

// Add method adds the serviceable to the given Cache object.
func (c *Cache) Add(serviceable iface.Serviceable) (iface.Serviceable, error) {
	prefix := serviceable.Matcher().CachePrefix()

	c.locker.RLock()
	servList, ok := c.cacheMap[prefix]
	c.locker.RUnlock()
	if ok {
		serv, ok := servList[serviceable.Id()]
		if ok {
			if serv.Match(serviceable.Matcher()) == matcherSpec.HighMatch {
				return serv, nil
			}
		}
	} else {
		c.locker.Lock()
		c.cacheMap[prefix] = make(map[string]iface.Serviceable)
		c.locker.Unlock()
	}

	if err := serviceable.Validate(serviceable.Matcher()); err != nil {
		return nil, fmt.Errorf("validating serviceable failed with: %s", err)
	}

	c.locker.Lock()
	c.cacheMap[prefix][serviceable.Id()] = serviceable
	c.locker.Unlock()

	return serviceable, nil
}

// Get method gets the list of serviceables from the cache map, where the serviceables that are returned are those with a high match for given match definition.
func (c *Cache) Get(matcher iface.MatchDefinition) ([]iface.Serviceable, error) {
	var serviceables []iface.Serviceable

	c.locker.RLock()
	servList, ok := c.cacheMap[matcher.CachePrefix()]
	c.locker.RUnlock()
	if ok {
		for _, serviceable := range servList {
			if serviceable.Match(matcher) == matcherSpec.HighMatch {
				serviceables = append(serviceables, serviceable)
			}
		}
	}

	if len(serviceables) < 1 {
		return nil, fmt.Errorf("getting cached serviceable from matcher %v, failed with: does not exist", matcher)
	}

	return serviceables, nil
}

// Remove removes a single serviceable from the cache.
func (c *Cache) Remove(serviceable iface.Serviceable) {
	c.locker.Lock()
	delete(c.cacheMap[serviceable.Matcher().CachePrefix()], serviceable.Id())
	c.locker.Unlock()
}

// Validate method checks to see if the serviceable commit matches the current commit.
func (c *Cache) Validate(serviceables []iface.Serviceable, branch string, tns tns.Client) error {
	if len(serviceables) > 0 {
		project, err := serviceables[0].Project()
		if err != nil {
			return fmt.Errorf("validating cached pick project id failed with: %s", err)
		}

		commit, err := tns.Simple().Commit(project.String(), branch)
		if err != nil {
			return err
		}

		for _, serviceable := range serviceables {
			if serviceable.Commit() != commit {
				return fmt.Errorf("cached pick commit `%s` is outdated, latest commit is `%s`", serviceable.Commit(), commit)
			}

		}

	}

	return nil
}
