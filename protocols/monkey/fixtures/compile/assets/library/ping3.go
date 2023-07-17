package lib

import (
	"github.com/taubyte/go-sdk/event"
)

//export ping3
func ping3(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}
	h.Write([]byte("PONG3"))

	return 0
}
