//go:build example

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"

	"github.com/taubyte/go-sdk/event"
	http "github.com/taubyte/go-sdk/http/event"
)

//export exampletest
func exampletest(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	h.Write([]byte("PONG"))

	err = runExampleTest(h)
	if err != nil {
		h.Write([]byte(fmt.Sprintf("Example test failed with: %s", err)))
		return 1
	}

	return 0
}

func runExampleTest(h http.Event) error {
	fmt.Println("Ran example")

	return nil
}
