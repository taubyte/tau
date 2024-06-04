package pubsub

import (
	"context"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/taubyte/tau/core/services/substrate/components"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

type Service interface {
	components.ServiceComponent
	Subscribe(projectId, appId, channel string) error
	Publish(ctx context.Context, projectId, appId, channel string, data []byte) error
	WebSocketURL(projectId, appId, channel string) (string, error)
}

type Messaging interface {
	Config() *structureSpec.Messaging
}

type Serviceable interface {
	components.FunctionServiceable
	HandleMessage(msg *pubsub.Message) (time.Time, error)
	Name() string
}

type Channel interface {
	Context() context.Context
	SmartOps(smartOps []string) (uint32, error)
	Type() uint32
	Messaging
}
