package pubsub

import (
	"github.com/gorilla/websocket"
	service "github.com/taubyte/http"
	"github.com/taubyte/odo/protocols/node/components/pubsub/common"
	_websocket "github.com/taubyte/odo/protocols/node/components/pubsub/websocket"
)

func (s *Service) attach() {
	s.Http().WebSocket(&service.WebSocketDefinition{
		Path: common.WebSocketHttpPath,
		Vars: service.Variables{
			Required: []string{
				"hash", "channel",
			},
		},
		NewHandler: func(ctx service.Context, conn *websocket.Conn) service.WebSocketHandler {
			return _websocket.Handler(s, ctx, conn)
		},
	})
}
