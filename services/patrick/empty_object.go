package service

import (
	"github.com/taubyte/tau/core/services/tns"
	spec "github.com/taubyte/tau/pkg/specs/common"
)

type emptyObject struct{}

func (e *emptyObject) Path() tns.Path {
	return spec.NewTnsPath([]string{})
}

func (e *emptyObject) Bind(interface{}) error {
	return nil
}

func (e *emptyObject) Interface() interface{} {
	return nil
}

func (e *emptyObject) Current(branch []string) ([]tns.Path, error) {
	return nil, nil
}

func newEmptyObject() tns.Object {
	return &emptyObject{}
}
