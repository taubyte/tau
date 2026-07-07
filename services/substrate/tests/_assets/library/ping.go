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

	return 0
}

// getHandler and postHandler let a test tell which function served a request
// when two functions share a path but differ by method (issue #340).

//export getHandler
func getHandler(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}
	h.Write([]byte("GET-HANDLER"))

	return 0
}

//export postHandler
func postHandler(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}
	h.Write([]byte("POST-HANDLER"))

	return 0
}
