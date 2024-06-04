package structure

import (
	"fmt"

	"github.com/taubyte/tau/pkg/specs/methods"
)

func (c *Structure[T]) getById(branch, commit, projectId, appId, resourceId string) (resource T, err error) {
	key, err := methods.GetBasicTNSKey(branch, commit, projectId, appId, resourceId, c.variable)
	if err != nil {
		return
	}

	resourcesInterface, err := c.tns.Fetch(key)
	if err != nil {
		return
	}

	err = resourcesInterface.Bind(&resource)
	if resource == nil {
		err = fmt.Errorf("resource (%T) with ID %s not found", resource, resourceId)
	} else {
		resource.SetId(resourceId)
	}

	return
}

func (c RelativeClient[T]) GetById(resourceId string) (T, error) {
	commit, err := c.Commit(c.projectId, c.branch)
	if err != nil {
		return nil, err
	}

	return c.getById(c.branch, commit, c.projectId, c.appId, resourceId)
}

func (c RelativeClient[T]) GetByIdCommit(resourceId string, commit string) (resource T, err error) {
	resource, err = c.getById(c.branch, commit, c.projectId, "", resourceId)
	if resource == nil {
		resource, err = c.getById(c.branch, commit, c.projectId, c.appId, resourceId)
	}

	return
}

func (c AllClient[T]) GetById(resourceId string) (resource T, err error) {
	commit, err := c.Commit(c.projectId, c.branch)
	if err != nil {
		return nil, err
	}

	return c.GetByIdCommit(resourceId, commit)
}

func (c AllClient[T]) GetByIdCommit(resourceId string, commit string) (resource T, err error) {
	resource, err = c.getById(c.branch, commit, c.projectId, "", resourceId)
	if resource == nil {
		resource, err = c.getById(c.branch, commit, c.projectId, c.appId, resourceId)
	}

	return
}

func (c GlobalClient[T]) GetById(resourceId string) (T, error) {
	commit, err := c.Commit(c.projectId, c.branch)
	if err != nil {
		return nil, err
	}

	return c.getById(c.branch, commit, c.projectId, "", resourceId)
}

func (c GlobalClient[T]) GetByIdCommit(resourceId string, commit string) (resource T, err error) {
	resource, err = c.getById(c.branch, commit, c.projectId, "", resourceId)
	if resource == nil {
		resource, err = c.getById(c.branch, commit, c.projectId, "", resourceId)
	}

	return
}
