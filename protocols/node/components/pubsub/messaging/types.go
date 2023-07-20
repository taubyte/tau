package messaging

import (
	"context"

	"github.com/taubyte/go-interfaces/services/substrate"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/pubsub"
	"github.com/taubyte/odo/protocols/node/components/pubsub/common"
)

// For running smartOps of a messaging channel before running a function itself.
type Channel struct {
	ctx   context.Context
	_type uint32

	*common.MessagingItem
	srv iface.Service
}

var _ substrate.SmartOpEventCaller = &Channel{}
var _ iface.Channel = &Channel{}

func (c *Channel) Type() uint32 {
	return c._type
}

func (c *Channel) SmartOps(smartOps []string) (uint32, error) {
	return c.srv.SmartOps().Run(c, smartOps)
}

func (c *Channel) Context() context.Context {
	return c.ctx
}
