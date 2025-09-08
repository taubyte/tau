package websocket

import (
	"context"
	"fmt"

	"github.com/gorilla/websocket"
	"github.com/taubyte/tau/core/services/substrate/components"
	iface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
)

type dataStreamHandler struct {
	ctx     context.Context
	ctxC    context.CancelFunc
	conn    WebSocketConnection //*websocket.Conn
	ch      chan []byte
	errCh   chan error
	srv     common.LocalService
	matcher components.MatchDefinition // *common.MatchDefinition

	picks []iface.Serviceable
}

func (h *dataStreamHandler) Close() {
	h.ctxC()
	close(h.ch)
	close(h.errCh)
}

func (h *dataStreamHandler) Error(err error) {
	select {
	case h.errCh <- err:
	default:
	}
}

func (h *dataStreamHandler) In() {
	for {
		select {
		case <-h.ctx.Done():
			return
		default:
			_, msg, err := h.conn.ReadMessage()
			if err != nil {
				h.Error(fmt.Errorf("reading data In on `%s` failed with: %s", h.matcher, err))
				return
			}

			err = h.srv.Node().PubSubPublish(h.ctx, h.matcher.String(), msg)
			if err != nil {
				h.Error(fmt.Errorf("reading data In then Publish failed with: %v", err))
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
		case err := <-h.errCh:
			h.conn.WriteJSON(WrappedMessage{
				Error: fmt.Sprintf("Writing data out failed with %v closing connection", err),
			})
			h.conn.Close()
			return
		case data := <-h.ch:
			err := h.conn.WriteMessage(websocket.BinaryMessage, data)
			if err != nil {
				h.Error(fmt.Errorf("writing data out failed with %v closing connection", err))
				return
			}
		}
	}
}
