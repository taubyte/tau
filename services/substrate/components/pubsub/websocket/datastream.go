package websocket

import (
	"context"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/taubyte/tau/core/services/substrate/components"
	service "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
)

type dataStreamHandler struct {
	ctx     context.Context
	ctxC    context.CancelFunc
	conn    service.WebSocketConnection
	ch      chan []byte
	errCh   chan error
	srv     common.LocalService
	matcher components.MatchDefinition

	mu sync.RWMutex
}

func (h *dataStreamHandler) Close() {
	h.ctxC()
	h.mu.Lock()
	defer h.mu.Unlock()

	// Close WebSocket connection
	if h.conn != nil {
		h.conn.Close()
	}

	// Close and clear channels
	if h.ch != nil {
		close(h.ch)
		h.ch = nil
	}
	if h.errCh != nil {
		close(h.errCh)
		h.errCh = nil
	}
}

func (h *dataStreamHandler) error(err error) {
	select {
	case h.errCh <- err:
	default:
	}
}

func (h *dataStreamHandler) In() {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for {
		select {
		case <-h.ctx.Done():
			return
		default:
			// ReadMessage blocks until a message is received
			// This is better than a busy loop as it actually blocks
			_, msg, err := h.conn.ReadMessage()
			if err != nil {
				h.error(fmt.Errorf("reading data In on `%s` failed with: %s", h.matcher, err))
				return
			}

			err = h.srv.Node().PubSubPublish(h.ctx, h.matcher.String(), msg)
			if err != nil {
				h.error(fmt.Errorf("reading data In then Publish failed with: %v", err))
				return
			}
		}
	}
}

func (h *dataStreamHandler) Out() {
	h.mu.RLock()
	defer h.mu.RUnlock()
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
				h.error(fmt.Errorf("writing data out failed with %v closing connection", err))
				return
			}
		}
	}
}
