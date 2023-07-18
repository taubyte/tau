package tvm

import (
	commonIface "github.com/taubyte/go-interfaces/services/substrate/common"
	structureSpec "github.com/taubyte/go-specs/structure"
)

var (
	_ commonIface.Function         = &Function{}
	_ commonIface.FunctionInstance = &FunctionInstance{}
)

type Function struct {
	srv         commonIface.Service
	serviceable commonIface.Serviceable
}

func (f *Function) Verbose() bool {
	return f.srv.Verbose()
}

type FunctionInstance struct {
	parent      *Function
	path        string
	project     string
	application string
	config      structureSpec.Function
}

func (f *FunctionInstance) Function() commonIface.Function {
	return f.parent
}
