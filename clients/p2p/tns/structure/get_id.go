package structure

import (
	"errors"
	"fmt"

	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
)

func (c *Structure[T]) getById(branches []string, commit, projectId, appId, resourceId string) (resource T, err error) {
	var (
		key                *common.TnsPath
		resourcesInterface tns.Object
	)

	for _, branch := range branches {
		key, err = methods.GetBasicTNSKey(branch, commit, projectId, appId, resourceId, c.variable)
		if err != nil {
			continue
		}

		resourcesInterface, err = c.tns.Fetch(key)
		if err != nil {
			continue
		}

		err = resourcesInterface.Bind(&resource)
		if resource == nil {
			err = fmt.Errorf("resource (%T) with ID %s not found", resource, resourceId)
		} else {
			resource.SetId(resourceId)
			break
		}
	}

	if resource == nil {
		err = errors.New("not found")
	}

	return
}

func (c RelativeClient[T]) GetById(resourceId string) (T, error) {
	commit, branch, err := c.Commit(c.projectId)
	if err != nil {
		return nil, err
	}

	return c.getById([]string{branch}, commit, c.projectId, c.appId, resourceId)
}

func (c RelativeClient[T]) GetByIdCommit(resourceId string, commit string) (resource T, err error) {
	resource, err = c.getById(c.branches, commit, c.projectId, "", resourceId)
	if resource == nil {
		resource, err = c.getById(c.branches, commit, c.projectId, c.appId, resourceId)
	}

	return
}

func (c AllClient[T]) GetById(resourceId string) (resource T, err error) {
	commit, branch, err := c.Commit(c.projectId)
	if err != nil {
		return nil, err
	}

	resource, err = c.getById([]string{branch}, commit, c.projectId, "", resourceId)
	if resource == nil {
		resource, err = c.getById([]string{branch}, commit, c.projectId, c.appId, resourceId)
	}

	return
}

func (c AllClient[T]) GetByIdCommit(resourceId string, commit string) (resource T, err error) {
	resource, err = c.getById(c.branches, commit, c.projectId, "", resourceId)
	if resource == nil {
		resource, err = c.getById(c.branches, commit, c.projectId, c.appId, resourceId)
	}

	return
}

func (c GlobalClient[T]) GetById(resourceId string) (T, error) {
	commit, branch, err := c.Commit(c.projectId)
	if err != nil {
		return nil, err
	}

	return c.getById([]string{branch}, commit, c.projectId, "", resourceId)
}

func (c GlobalClient[T]) GetByIdCommit(resourceId string, commit string) (resource T, err error) {
	resource, err = c.getById(c.branches, commit, c.projectId, "", resourceId)
	if resource == nil {
		resource, err = c.getById(c.branches, commit, c.projectId, "", resourceId)
	}

	return
}
