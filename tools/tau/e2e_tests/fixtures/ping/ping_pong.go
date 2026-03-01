package main

import (
	"strconv"

	"github.com/taubyte/go-sdk/event"
)

//export ping
func ping(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	n := int64(0)
	if nStr, err := h.Query().Get("n"); err == nil && nStr != "" {
		n, _ = strconv.ParseInt(nStr, 10, 64)
	}

	h.Write([]byte("PONG"))
	h.Write([]byte(strconv.FormatInt(n+1, 10)))

	return 0
}
