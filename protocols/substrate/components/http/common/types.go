package common

import (
	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
)

var _ commonIface.MatchDefinition = &MatchDefinition{}

func New(host, path, method string) *MatchDefinition {
	return &MatchDefinition{
		Host:   host,
		Path:   path,
		Method: method,
		params: make(map[string]string, 0),
	}
}

// TODO: Maybe move this to interfaces?
type MatchDefinition struct {
	Host   string
	Path   string
	Method string
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
