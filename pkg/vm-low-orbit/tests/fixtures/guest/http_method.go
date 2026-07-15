//go:build http_method

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"

	"github.com/taubyte/go-sdk/event"
)

//export methodHttp
func methodHttp(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		fmt.Println("ERR", err)
		return 1
	}

	_, err = h.Write([]byte("Success"))
	if err != nil {
		fmt.Println("ERR", err)
		return 1
	}

	return 0
}
