package structure

import (
	"fmt"

	"github.com/taubyte/tau/pkg/specs/methods"
)

func (c *Structure[T]) list(branch, commit, projectId, appId string) (map[string]T, error) {
	key, err := methods.GetEmptyTNSKey(branch, commit, projectId, appId, c.variable)
	if err != nil {
		return nil, err
	}

	resourcesInterface, err := c.tns.Fetch(key)
	if err != nil {
		return nil, err
	}

	resourceMap := map[string]T{}
	err = resourcesInterface.Bind(&resourceMap)
	if err != nil {
		return nil, err
	}

	for id, resource := range resourceMap {
		resource.SetId(id)
	}

	return resourceMap, nil
}

/*
Note: Id is not filled in the structures as it's map[Id]T
*/
func (c RelativeClient[T]) List() (map[string]T, error) {
	commit, err := c.Commit(c.projectId, c.branch)
	if err != nil {
		return nil, err
	}

	return c.list(c.branch, commit, c.projectId, c.appId)
}

/*
Note: Id is not filled in the structures as it's map[Id]T
*/
func (c AllClient[T]) List() (map[string]T, error) {
	commit, err := c.Commit(c.projectId, c.branch)
	if err != nil {
		return nil, err
	}

	resourceMap, err := c.list(c.branch, commit, c.projectId, "")
	if err != nil {
		return nil, err
	}

	if len(c.appId) > 0 {
		applicationIdResourceMap, err := c.list(c.branch, commit, c.projectId, c.appId)
		if err != nil {
			return nil, err
		}

		for k, v := range applicationIdResourceMap {
			if _, ok := resourceMap[k]; ok {
				return nil, fmt.Errorf("found matching id in project(%s): %s", c.projectId, k)
			}
			resourceMap[k] = v
		}
	}

	return resourceMap, nil
}

/*
Note: Id is not filled in the structures as it's map[Id]T
*/
func (c GlobalClient[T]) List() (map[string]T, error) {
	commit, err := c.Commit(c.projectId, c.branch)
	if err != nil {
		return nil, err
	}

	return c.list(c.branch, commit, c.projectId, "")
}
