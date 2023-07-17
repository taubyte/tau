package common

import (
	"github.com/taubyte/go-interfaces/services/tns"
	spec "github.com/taubyte/go-specs/common"
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

func (e *emptyObject) Current(branch string) ([]tns.Path, error) {
	return nil, nil
}

func NewEmptyObject() tns.Object {
	return &emptyObject{}
}
