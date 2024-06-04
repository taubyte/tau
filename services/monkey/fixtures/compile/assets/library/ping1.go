package lib

import (
	"github.com/taubyte/go-sdk/event"
)

//export ping1
func ping1(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}
	h.Write([]byte("PONG1"))

	return 0
}
