//go:build redirect

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"

	"github.com/taubyte/go-sdk/event"
	http "github.com/taubyte/go-sdk/http/event"
)

//export redirecttest
func redirecttest(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	err = runRedirectTest(h)
	if err != nil {
		h.Write([]byte(fmt.Sprintf("Redirect test failed with: %s", err)))
		return 1
	}

	return 0
}

func runRedirectTest(h http.Event) error {
	return h.Redirect("https://p2p.skelouse.com/ping").Temporary()
}
