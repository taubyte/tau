package pubsub

import (
	"context"
	"time"

	"github.com/taubyte/tau/core/services/substrate/components"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

type Service interface {
	components.ServiceComponent
	Subscribe(projectId, appId, resource, channel string) error
	Publish(ctx context.Context, projectId, appId, resource, channel string, data []byte) error
	WebSocketURL(projectId, appId, channel string) (string, error)
}

type Messaging interface {
	Config() *structureSpec.Messaging
}

type Message interface {
	GetSource() string
	GetData() []byte
	GetTopic() string
	Marshal() ([]byte, error)
}

type MatchDefinition interface {
	String() string
	CachePrefix() string
	GenerateSocketURL() string
}

type Serviceable interface {
	components.FunctionServiceable
	HandleMessage(msg Message) (time.Time, error)
	Name() string
}

type Channel interface {
	Context() context.Context
	SmartOps(smartOps []string) (uint32, error)
	Type() uint32
	Messaging
}

type ServiceWithLookup interface {
	Service
	Lookup(matcher MatchDefinition) (picks []Serviceable, err error)
	Context() context.Context
}
