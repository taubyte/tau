package common

import (
	"context"
	"fmt"

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
	return fmt.Sprintf("%s/%s", multihash.Hash(m.Project+m.Application), m.Channel)
	// return m.Channel + m.Project + m.Application + strconv.FormatBool(m.WebSocket)
}

func (m *MatchDefinition) CachePrefix() string {
	return m.Project
}

type LocalService interface {
	iface.Service
	Lookup(matcher *MatchDefinition) (picks []iface.Serviceable, err error)
	Context() context.Context
}
