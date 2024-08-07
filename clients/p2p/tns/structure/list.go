package structure

import (
	"fmt"

	"github.com/taubyte/tau/pkg/specs/methods"
)

func (c *Structure[T]) list(branch, commit, projectId, appId string) (map[string]T, string, string, error) { //(o map[string]T, commit string, branch string, err error)
	key, err := methods.GetEmptyTNSKey(branch, commit, projectId, appId, c.variable)
	if err != nil {
		return nil, commit, branch, err
	}

	resourcesInterface, err := c.tns.Fetch(key)
	if err != nil {
		return nil, commit, branch, err
	}

	resourceMap := map[string]T{}
	err = resourcesInterface.Bind(&resourceMap)
	if err != nil {
		return nil, commit, branch, err
	}

	for id, resource := range resourceMap {
		resource.SetId(id)
	}

	return resourceMap, commit, branch, nil
}

/*
Note: Id is not filled in the structures as it's map[Id]T
*/
func (c RelativeClient[T]) List() (map[string]T, string, string, error) {
	commit, branch, err := c.Commit(c.projectId)
	if err != nil {
		return nil, commit, branch, err
	}

	return c.list(branch, commit, c.projectId, c.appId)
}

/*
Note: Id is not filled in the structures as it's map[Id]T
*/
func (c AllClient[T]) List() (map[string]T, string, string, error) {
	commit, branch, err := c.Commit(c.projectId)
	if err != nil {
		return nil, commit, branch, err
	}

	resourceMap, _, _, err := c.list(branch, commit, c.projectId, "")
	if err != nil {
		return nil, commit, branch, err
	}

	if len(c.appId) > 0 {
		applicationIdResourceMap, _, _, err := c.list(branch, commit, c.projectId, c.appId)
		if err != nil {
			return nil, commit, branch, err
		}

		for k, v := range applicationIdResourceMap {
			if _, ok := resourceMap[k]; ok {
				return nil, commit, branch, fmt.Errorf("found matching id in project(%s): %s", c.projectId, k)
			}
			resourceMap[k] = v
		}
	}

	return resourceMap, commit, branch, nil
}

/*
Note: Id is not filled in the structures as it's map[Id]T
*/
func (c GlobalClient[T]) List() (map[string]T, string, string, error) {
	commit, branch, err := c.Commit(c.projectId)
	if err != nil {
		return nil, commit, branch, err
	}

	return c.list(branch, commit, c.projectId, "")
}
