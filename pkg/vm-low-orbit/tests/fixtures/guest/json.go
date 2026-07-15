//go:build json

package main

//lint:file-ignore U1000 compiled file

import (
	"encoding/json"
	"fmt"

	"github.com/taubyte/go-sdk/event"
	http "github.com/taubyte/go-sdk/http/event"
)

type Foo struct {
	UUID  string         `json:"UUID"`
	State string         `json:"State"`
	Titus map[string]Foo `json:"Titus"`
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

	j, err := json.Marshal(f)
	if err != nil {
		return err
	}

	f0 := &Foo{}

	err = json.Unmarshal(j, f0)
	if err != nil {
		return err
	}

	h.Write(j)

	return nil
}
