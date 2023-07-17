package structure

func (c *Structure[T]) getByName(branch, projectId, appId, resourceName string) (resource T, err error) {
	commit, err := c.Commit(projectId, branch)
	if err != nil {
		return nil, err
	}

	resourceMap, err := c.list(branch, commit, projectId, appId)
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
	return c.getByName(c.branch, c.projectId, c.appId, resourceName)
}

func (c AllClient[T]) GetByName(resourceName string) (resource T, err error) {
	resource, err = c.getByName(c.branch, c.projectId, "", resourceName)
	if err == nil && resource == nil {
		resource, err = c.getByName(c.branch, c.projectId, c.appId, resourceName)
	}

	return
}

func (c GlobalClient[T]) GetByName(resourceName string) (resource T, err error) {
	return c.getByName(c.branch, c.projectId, "", resourceName)
}
