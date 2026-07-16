//go:build globals

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"

	"github.com/taubyte/go-sdk/event"
	"github.com/taubyte/go-sdk/globals/f32"
	"github.com/taubyte/go-sdk/globals/f64"
	"github.com/taubyte/go-sdk/globals/scope"
	"github.com/taubyte/go-sdk/globals/str"
	"github.com/taubyte/go-sdk/globals/u32"
	http "github.com/taubyte/go-sdk/http/event"
)

//export globaltest
func globaltest(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	err = runGlobalTest(h)
	if err != nil {
		h.Write([]byte(fmt.Sprintf("Global test failed with: %s", err)))
		return 1
	}

	return 0
}

func runGlobalTest(h http.Event) error {
	// Ignoring error on creation
	u, err := u32.GetOrCreate("hello", scope.Function)
	if err != nil {
		return err
	}

	err = u.Set(u.Value() + 4)
	if err != nil {
		return err
	}

	v2, err := u32.Get("hello", scope.Function)
	if err != nil {
		return err
	}

	s, err := str.GetOrCreate("hello")
	if err != nil {
		return err
	}

	err = s.Set(s.Value() + "Hello")
	if err != nil {
		return err
	}

	err = s.Set(s.Value() + ", world!")
	if err != nil {
		return err
	}

	s2, err := str.Get("hello")
	if err != nil {
		return err
	}

	s2Val := s2.Value()

	err = s2.Set("")
	if err != nil {
		return err
	}

	s3, err := str.Get("hello")
	if err != nil {
		return err
	}

	p1, err := f32.GetOrCreate("pie")
	if err != nil {
		return err
	}

	err = p1.Set(3.14)
	if err != nil {
		return err
	}

	p2, err := f64.GetOrCreate("pie")
	if err != nil {
		return err
	}

	err = p2.Set(3.14 * 2)
	if err != nil {
		return err
	}

	pie1, err := f32.Get("pie")
	if err != nil {
		return err
	}

	pie2, err := f64.Get("pie")
	if err != nil {
		return err
	}

	_, err = h.Write([]byte("{" +
		fmt.Sprintf(`"val1": %d,`, u.Value()) +
		fmt.Sprintf(`"val2": %d,`, v2.Value()) +
		fmt.Sprintf(`"stringVal1": "%s",`, s.Value()) +
		fmt.Sprintf(`"stringVal2": "%s",`, s2Val) +
		fmt.Sprintf(`"stringVal3": "%s",`, s3.Value()) +
		fmt.Sprintf(`"pie1": %v,`, pie1.Value()) +
		fmt.Sprintf(`"pie2": %v`, pie2.Value()) +
		"}"))

	return err
}
