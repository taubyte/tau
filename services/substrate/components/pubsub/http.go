package pubsub

import (
	service "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
	_websocket "github.com/taubyte/tau/services/substrate/components/pubsub/websocket"
)

func (s *Service) attach() {
	s.Http().WebSocket(&service.WebSocketDefinition{
		Path: common.WebSocketHttpPath,
		Vars: service.Variables{
			Required: []string{
				"hash", "channel",
			},
		},
		NewHandler: func(ctx service.Context, conn service.WebSocketConnection) service.WebSocketHandler {
			return _websocket.Handler(s, ctx, conn)
		},
	})
}
