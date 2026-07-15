//go:build basic

package main

import "github.com/taubyte/go-sdk/event"

//lint:ignore U1000 compiled file

//export basic
func basic(e event.Event) {
	h, err := e.HTTP()
	if err != nil {
		return
	}

	h.Write([]byte("hello world"))
}
