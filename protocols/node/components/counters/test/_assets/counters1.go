package lib

import (
	"github.com/taubyte/go-sdk/event"
)

//export countertest
func countertest(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	h.Write([]byte("pong"))
	return 0
}
