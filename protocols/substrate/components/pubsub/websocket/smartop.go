package websocket

import (
	"errors"
	"fmt"

	sdkSmartOpsCommon "github.com/taubyte/go-sdk-smartops/common"
	"github.com/taubyte/tau/protocols/substrate/components/pubsub/messaging"
)

func (h *dataStreamHandler) SmartOps() error {
	for _, pick := range h.picks {
		ws, ok := pick.(*WebSocket)
		if !ok {
			// Should not get here, as matcher has websocket:true, this is a precaution
			return errors.New("tried to run a smartOp on a websocket that was not a websocket")
		}

		for _, ch := range ws.mmi.Items {
			smartOps := ch.Config().SmartOps
			if len(smartOps) > 0 {
				m, err := messaging.New(
					h.ctx,
					nil, // No event from opening a websocket
					uint32(sdkSmartOpsCommon.ResourceTypeMessagingWebSocket),
					h.srv,
					ch,
				)

				// TODO, should not error here, should remove from the mmi, then continue running the function with
				// the reduced mmi, well maybe it should as the other side may be listening to one that the match failed
				// for,  future iteration.
				if err != nil {
					return err
				}

				val, err := m.SmartOps(smartOps)
				if err != nil || val > 0 {
					if err != nil {
						return err
					}
					return fmt.Errorf("exited: %d", val)
				}
			}
		}
	}

	return nil
}
