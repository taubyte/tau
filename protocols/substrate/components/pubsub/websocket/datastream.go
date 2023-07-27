package websocket

import (
	"context"
	"fmt"

	"github.com/gorilla/websocket"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/pubsub"
	"github.com/taubyte/tau/protocols/substrate/components/pubsub/common"
)

type dataStreamHandler struct {
	ctx     context.Context
	ctxC    context.CancelFunc
	conn    *websocket.Conn
	ch      chan []byte
	srv     common.LocalService
	matcher *common.MatchDefinition

	picks []iface.Serviceable
}

func (h *dataStreamHandler) Close() {
	if h.ctx.Err() == nil {
		h.ctxC()
	}
	close(h.ch)
}

func (h *dataStreamHandler) In() {
	for {
		select {
		case <-h.ctx.Done():
			return
		default:
			_, msg, err := h.conn.ReadMessage()
			if err != nil {
				h.conn.WriteJSON(WrappedMessage{
					Error: fmt.Sprintf("reading data In on `%s` failed with: %s", h.matcher.Path(), err),
				})
				h.conn.Close()
				return
			}

			err = h.srv.Node().PubSubPublish(h.ctx, h.matcher.Path(), msg)
			if err != nil {
				h.conn.WriteJSON(WrappedMessage{
					Error: fmt.Sprintf("reading data In then Publish failed with: %v", err),
				})
				h.conn.Close()
				return
			}
		}
	}
}

func (h *dataStreamHandler) Out() {
	for {
		select {
		case <-h.ctx.Done():
			return
		case data := <-h.ch:
			err := h.conn.WriteMessage(websocket.BinaryMessage, data)
			if err != nil {
				h.conn.WriteJSON(WrappedMessage{
					Error: fmt.Sprintf("Writing data out failed with %v closing connection", err),
				})
				h.conn.Close()
				h.ctxC()
				return
			}
		}
	}
}
