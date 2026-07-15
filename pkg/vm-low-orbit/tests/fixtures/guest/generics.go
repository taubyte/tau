//go:build generics

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"

	"github.com/taubyte/go-sdk/event"
	http "github.com/taubyte/go-sdk/http/event"
)

type Number[T any] interface {
	Add(T) error
	Sub(T) error
	Value() T
}

type _int32 struct {
	value int32
	name  string
}

func (i *_int32) Add(v int32) error {
	i.value += v
	return nil
}

func (i *_int32) Sub(v int32) error {
	i.value -= v
	return nil
}

func (i *_int32) Value() int32 {
	return i.value
}

func Int32(name string) Number[int32] {
	return &_int32{name: name}
}

//export generictest
func generictest(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	err = runGenericTest(h)
	if err != nil {
		h.Write([]byte(fmt.Sprintf("Generic test failed with: %s", err)))
		return 1
	}

	return 0
}

func runGenericTest(h http.Event) error {
	v := Int32("something")

	err := v.Add(10)
	if err != nil {
		return err
	}

	err = v.Add(2)
	if err != nil {
		return err
	}
	_, err = h.Write([]byte(fmt.Sprintf("%d", v.Value())))
	return err
}
