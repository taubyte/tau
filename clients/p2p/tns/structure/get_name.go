package structure

func (c *Structure[T]) getByName(branches []string, projectId, appId, resourceName string) (resource T, err error) {
	commit, branch, err := c.Commit(projectId, branches...)
	if err != nil {
		return nil, err
	}

	resourceMap, _, _, err := c.list(branch, commit, projectId, appId)
	if err != nil {
		return
	}

	for _, _resource := range resourceMap {
		if _resource.GetName() == resourceName {
			resource = _resource
			break
		}
	}

	return
}

func (c RelativeClient[T]) GetByName(resourceName string) (resource T, err error) {
	return c.getByName(c.branches, c.projectId, c.appId, resourceName)
}

func (c RelativeClient[T]) Commit(projectId string) (commit, branch string, err error) {
	return c.Structure.Commit(projectId, c.branches...)
}

func (c AllClient[T]) GetByName(resourceName string) (resource T, err error) {
	resource, err = c.getByName(c.branches, c.projectId, "", resourceName)
	if err == nil && resource == nil {
		resource, err = c.getByName(c.branches, c.projectId, c.appId, resourceName)
	}

	return
}

func (c AllClient[T]) Commit(projectId string) (commit, branch string, err error) {
	return c.Structure.Commit(projectId, c.branches...)
}

func (c GlobalClient[T]) GetByName(resourceName string) (resource T, err error) {
	return c.getByName(c.branches, c.projectId, "", resourceName)
}

func (c GlobalClient[T]) Commit(projectId string) (commit, branch string, err error) {
	return c.Structure.Commit(projectId, c.branches...)
}
