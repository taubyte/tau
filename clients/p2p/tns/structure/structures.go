package structure

import "github.com/taubyte/tau/core/services/tns"

/*
Search globally
*/
func (c *Structure[T]) Global(projectId string, branches ...string) tns.StructureGetter[T] {
	return GlobalClient[T]{c, projectId, branches}
}

/*
Search relative to the application provided
*/
func (c *Structure[T]) Relative(projectId, appId string, branches ...string) tns.StructureGetter[T] {
	return RelativeClient[T]{c, projectId, appId, branches}
}

/*
Search relative to the application provided and globally
*/
func (c *Structure[T]) All(projectId, appId string, branches ...string) tns.StructureGetter[T] {
	return AllClient[T]{c, projectId, appId, branches}
}
