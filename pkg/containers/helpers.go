package containers

import "github.com/docker/docker/api/types/filters"

// New Filter returns a filter argument to perform key value Lookups on docker host.
func NewFilter(key, value string) filters.Args {
	filter := filters.NewArgs()
	filter.Add(key, value)

	return filter
}
