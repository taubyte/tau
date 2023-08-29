package tvm

import (
	"time"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/go-interfaces/vm"
	structureSpec "github.com/taubyte/go-specs/structure"
)

var MaxInstanceRequest = 1024 * 64
var ShadowBuff uint64 = 10
var ShadowTTL time.Duration = 15 * time.Minute

var (
	_ commonIface.Function         = &Function{}
	_ commonIface.FunctionInstance = &FunctionInstance{}
)

// This guy should be cached
type Function struct {
	serviceable commonIface.Serviceable

	instanceRequest chan instanceRequest

	instanceBuff chan *instanceShadow

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

type metricRuntime struct {
	vm.Runtime
	f *Function
}

func (f *FunctionInstance) Function() commonIface.Function {
	return f.parent
}
