//go:build json

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"

	"github.com/taubyte/go-sdk/event"
	http "github.com/taubyte/go-sdk/http/event"
)

//go:generate go get github.com/mailru/easyjson
//go:generate go install github.com/mailru/easyjson/...@latest
//go:generate easyjson -all ${GOFILE}

type Foo struct {
	UUID  string
	State string
	Titus map[string]Foo
}

//export jsontest
func jsontest(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	err = runJSONTest(h)
	if err != nil {
		h.Write([]byte(fmt.Sprintf("JSON test failed with: %s", err)))
		return 1
	}

	return 0
}

func runJSONTest(h http.Event) error {
	f := &Foo{
		UUID:  "ewefwefwe",
		State: "TX",
		Titus: map[string]Foo{
			"Ti1": {
				UUID: "qwdqwdqw",
			},
		},
	}

	j, err := f.MarshalJSON()
	if err != nil {
		return err
	}

	f0 := &Foo{}

	f0.UnmarshalJSON(j)

	h.Write(j)

	return nil
}
