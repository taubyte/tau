package tvm

import (
	"github.com/taubyte/go-interfaces/services/substrate"
	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	structureSpec "github.com/taubyte/go-specs/structure"
)

var (
	_ commonIface.Function         = &Function{}
	_ commonIface.FunctionInstance = &FunctionInstance{}
)

type Function struct {
	srv         substrate.Service
	serviceable commonIface.Serviceable
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
