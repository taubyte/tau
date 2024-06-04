package structure

import "github.com/taubyte/tau/core/services/tns"

/*
Search globally
*/
func (c *Structure[T]) Global(projectId, branch string) tns.StructureGetter[T] {
	return GlobalClient[T]{c, projectId, branch}
}

/*
Search relative to the application provided
*/
func (c *Structure[T]) Relative(projectId, appId, branch string) tns.StructureGetter[T] {
	return RelativeClient[T]{c, projectId, appId, branch}
}

/*
Search relative to the application provided and globally
*/
func (c *Structure[T]) All(projectId, appId, branch string) tns.StructureGetter[T] {
	return AllClient[T]{c, projectId, appId, branch}
}
