package lib

import (
	"github.com/taubyte/go-sdk/event"
)

//export ping
func ping(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}
	h.Write([]byte("PONG"))
	h.Return(200)

	return 0
}
