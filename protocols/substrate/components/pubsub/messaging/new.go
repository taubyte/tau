package messaging

import (
	"context"

	iface "github.com/taubyte/go-interfaces/services/substrate/components/pubsub"
	"github.com/taubyte/tau/protocols/substrate/components/pubsub/common"
)

func New(ctx context.Context, event interface{}, _type uint32, srv iface.Service, item *common.MessagingItem) (*Channel, error) {
	return &Channel{
		ctx:           ctx,
		_type:         _type,
		MessagingItem: item,
		srv:           srv,
	}, nil
}
