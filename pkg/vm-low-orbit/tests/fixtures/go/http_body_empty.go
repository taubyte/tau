//go:build http_body_empty

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"
	"io"

	"github.com/taubyte/go-sdk/event"
	http "github.com/taubyte/go-sdk/http/event"
)

//export eventbodyempty
func eventbodyempty(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	err = runEventBodyEmpty(h)
	if err != nil {
		h.Write([]byte(fmt.Sprintf("runEventBodyEmpty test failed with: %s", err)))
		return 1
	}

	return 0
}

func runEventBodyEmpty(h http.Event) error {
	_, err := io.ReadAll(h.Body())
	if err != nil {
		return fmt.Errorf("read body failed with: %s", err.Error())
	}

	return nil
}
