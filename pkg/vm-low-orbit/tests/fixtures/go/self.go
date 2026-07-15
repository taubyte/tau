//go:build self

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"

	"github.com/taubyte/go-sdk/event"
	http "github.com/taubyte/go-sdk/http/event"
	"github.com/taubyte/go-sdk/self"
)

//export selftest
func selftest(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	err = runSelfTest(h)
	if err != nil {
		h.Write([]byte(fmt.Sprintf("Self test failed with: %s", err)))
		return 1
	}

	return 0
}

func runSelfTest(h http.Event) error {
	id, err := self.Function()
	if err != nil {
		return err
	}

	project, err := self.Project()
	if err != nil {
		return err
	}

	application, err := self.Application()
	if err != nil {
		return err
	}

	commit, err := self.Commit()
	if err != nil {
		return err
	}

	branch, err := self.Branch()
	if err != nil {
		return err
	}

	_, err = h.Write([]byte(`{
"id": "` + id + `",
"project": "` + project + `",
"application": "` + application + `",
"commit": "` + commit + `",
"branch": "` + branch + `"
}`))

	return err
}
