package cache

import (
	"fmt"

	iface "github.com/taubyte/tau/core/services/substrate/components"
	spec "github.com/taubyte/tau/pkg/specs/common"
	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
)

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
		c.locker.RLock()
		serviceable, ok := servList[serviceable.Id()]
		c.locker.RUnlock()
		if ok {
			if serviceable.Match(serviceable.Matcher()) == matcherSpec.HighMatch {

				return serviceable, nil
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
func (c *Cache) Get(matcher iface.MatchDefinition, ops iface.GetOptions) ([]iface.Serviceable, error) {
	var serviceables []iface.Serviceable

	c.locker.RLock()
	servList, ok := c.cacheMap[matcher.CachePrefix()]
	c.locker.RUnlock()
	if ok {
		matchIndex := matcherSpec.HighMatch
		if ops.MatchIndex != nil {
			matchIndex = *ops.MatchIndex
		}
		branch := ops.Branches
		if len(branch) < 1 {
			branch = spec.DefaultBranches
		}

		for _, serviceable := range servList {
			if serviceable.Match(matcher) == matchIndex {
				if ops.Validation {
					if err := c.validate(serviceable, branch); err != nil {
						// remove serviceable from cache & continue
						c.Remove(serviceable)
						continue
					}
				}
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
	serviceable.Close()
}

// validate method checks to see if the serviceable commit matches the current commit.
func (c *Cache) validate(serviceable iface.Serviceable, branches []string) error {
	tnsClient := serviceable.Service().Tns()
	projectId := serviceable.Project()
	commit, _, err := tnsClient.Simple().Commit(projectId, branches...)
	if err != nil {
		return fmt.Errorf("getting serviceable `%s` commit failed with: %w", serviceable.Id(), err)
	}

	if serviceable.Commit() != commit {
		return fmt.Errorf("cached pick commit `%s` is outdated, latest commit is `%s`", serviceable.Commit(), commit)
	}

	cid, err := ResolveAssetCid(serviceable)
	if err != nil {
		return fmt.Errorf("getting cached serviceable `%s` cid failed with: %w", serviceable.Id(), err)
	}

	if serviceable.AssetId() != cid {
		return fmt.Errorf("serviceable `%s` asset is outdated", serviceable.Id())
	}

	return nil
}
