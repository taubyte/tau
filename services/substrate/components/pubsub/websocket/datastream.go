package websocket

import (
	"context"
	"fmt"

	"github.com/gorilla/websocket"
	"github.com/taubyte/tau/core/services/substrate/components"
	pubsubIface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	service "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
)

type dataStreamHandler struct {
	ctx     context.Context
	ctxC    context.CancelFunc
	conn    service.WebSocketConnection
	id      string
	ch      chan pubsubIface.Message
	errCh   chan error
	srv     pubsubIface.ServiceWithLookup
	matcher components.MatchDefinition
}

func (h *dataStreamHandler) Close() {
	h.ctxC()
	h.conn.Close()
}

func (h *dataStreamHandler) error(err error) {
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
				h.error(fmt.Errorf("reading data In on `%s` failed with: %s", h.matcher, err))
				return
			}

			message, err := common.NewMessage(msg, h.id)
			if err != nil {
				h.error(fmt.Errorf("creating message failed with: %w", err))
				return
			}
			msg, err = message.Marshal()
			if err != nil {
				h.error(fmt.Errorf("marshalling message failed with: %w", err))
				return
			}

			fmt.Println("WEBSOKCET IN message source >>>>>", message.GetSource())
			fmt.Println("WEBSOKCET IN message >>>>>", string(msg))

			err = h.srv.Node().PubSubPublish(h.ctx, h.matcher.String(), msg)
			if err != nil {
				h.error(fmt.Errorf("reading data In then Publish failed with: %v", err))
				return
			}
		}
	}
}

func (h *dataStreamHandler) Out() {
	defer h.Close()
	for {
		select {
		case <-h.ctx.Done():
			return
		case err := <-h.errCh:
			h.conn.WriteJSON(WrappedMessage{
				Error: fmt.Sprintf("Writing data out failed with %v closing connection", err),
			})
			return
		case data := <-h.ch:
			fmt.Println("PUBSUB to WEBSOKCET OUT message>>>>>", string(data.GetData()))
			fmt.Println("WEBSOKCET OUT message source>>>>>", data.GetSource())
			if data.GetSource() == h.id {
				fmt.Println("WEBSOKCET OUT message source is self>>>>>", data.GetSource())
				// ignore the message - comes from self
				continue
			}

			fmt.Println("WEBSOKCET OUT message>>>>>", string(data.GetData()))

			err := h.conn.WriteMessage(websocket.BinaryMessage, data.GetData())
			if err != nil {
				h.error(fmt.Errorf("writing data out failed with %v closing connection", err))
				return
			}
		}
	}
}
