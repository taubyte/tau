package tvm

import (
	"github.com/taubyte/go-interfaces/services/substrate"
	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/go-interfaces/vm"
	structureSpec "github.com/taubyte/go-specs/structure"
)

var MaxIntanceRequest = 1024 * 64
var ShadowBuff uint64 = 10

var (
	_ commonIface.Function         = &Function{}
	_ commonIface.FunctionInstance = &FunctionInstance{}
)

// This guy should be cached
type Function struct {
	srv         substrate.Service
	serviceable commonIface.Serviceable

	intanceRequest chan instanceRequest

	intanceBuff chan *instanceShadow

	// metrics -- helps keep a buffer
	instanceReqCount   uint64
	runtimeCount       uint64
	runtimeClosedCount uint64
}

type instanceRequest struct {
	ctx    commonIface.FunctionContext
	branch string
	commit string

	response chan<- instanceRequestResponse
}

type instanceShadow struct {
	fI      commonIface.FunctionInstance
	runtime vm.Runtime
	plugin  interface{}
}

type instanceRequestResponse struct {
	instanceShadow
	err error
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
