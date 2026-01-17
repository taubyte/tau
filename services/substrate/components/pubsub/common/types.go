package common

import (
	multihash "github.com/taubyte/tau/utils/multihash"
)

type MatchDefinition struct {
	Channel     string
	Project     string
	Application string
	WebSocket   bool
}

func (m *MatchDefinition) String() string {
	return multihash.Hash(m.Project+m.Application) + "/" + m.Channel
}

func (m *MatchDefinition) CachePrefix() string {
	return m.Project
}

func (m *MatchDefinition) GenerateSocketURL() string {
	return "ws-" + m.String()
}
