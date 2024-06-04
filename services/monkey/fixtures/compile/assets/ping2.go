package lib

import (
	"github.com/taubyte/go-sdk/event"
)

//export ping2
func ping2(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}
	h.Write([]byte("PONG2"))

	return 0
}
