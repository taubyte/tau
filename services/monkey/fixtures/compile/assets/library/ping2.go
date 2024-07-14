package lib

import (
	"github.com/taubyte/go-sdk/event"
)

//lint:ignore U1000 wasm export
//export ping2
func ping2(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}
	h.Write([]byte("PONG2"))

	return 0
}
