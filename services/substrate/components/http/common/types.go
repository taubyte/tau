package common

import (
	commonIface "github.com/taubyte/tau/core/services/substrate/components"
)

var _ commonIface.MatchDefinition = &MatchDefinition{}

func New(host, path, method string) *MatchDefinition {
	return &MatchDefinition{
		Request: &Request{
			Host:   host,
			Path:   path,
			Method: method,
		},
		params: make(map[string]string, 0),
	}
}

type Request struct {
	Host   string
	Path   string
	Method string
}

// TODO: Maybe move this to interfaces?
type MatchDefinition struct {
	*Request
	params map[string]string
}

func (m *MatchDefinition) String() string {
	return m.Host + m.Path + m.Method
}

func (m *MatchDefinition) CachePrefix() string {
	return m.Host
}

func (m *MatchDefinition) Set(key, value string) {
	m.params[key] = value
}

func (m *MatchDefinition) Get(key string) string {
	return m.params[key]
}

const PathMatch = "pathMatch"
