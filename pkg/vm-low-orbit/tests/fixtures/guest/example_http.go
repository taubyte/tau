//go:build example_http

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"

	"github.com/taubyte/go-sdk/event"
)

//export examplehttp
func examplehttp(e event.Event) uint32 {
	fmt.Println("EVENT", e)

	return 0
}
