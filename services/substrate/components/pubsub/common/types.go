package common

import (
	"context"

	iface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
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

type LocalService interface {
	iface.Service
	Lookup(matcher *MatchDefinition) (picks []iface.Serviceable, err error)
	Context() context.Context
}
