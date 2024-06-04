package common

type MatchDefinition struct {
	Project     string
	Application string
	Protocol    string
	Command     string
}

func (m *MatchDefinition) String() string {
	return m.Project + m.Application + m.Protocol + m.Command
}

func (m *MatchDefinition) CachePrefix() string {
	return m.Project
}
