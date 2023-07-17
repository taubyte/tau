package common

import (
	"context"
	"strconv"

	iface "github.com/taubyte/go-interfaces/services/substrate/pubsub"
)

type MatchDefinition struct {
	Channel     string
	Project     string
	Application string
	WebSocket   bool
	Commit      string
}

func (m *MatchDefinition) String() string {
	return m.Channel + m.Project + m.Application + strconv.FormatBool(m.WebSocket)
}

func (m *MatchDefinition) CachePrefix() string {
	return m.Project
}

type LocalService interface {
	iface.Service
	Lookup(matcher *MatchDefinition) (picks []iface.Serviceable, err error)
	Context() context.Context
}
