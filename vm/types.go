package tvm

import (
	"context"
	"time"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/go-interfaces/vm"
	structureSpec "github.com/taubyte/go-specs/structure"
)

var MaxInstanceRequest = 1024 * 64
var ShadowBuff uint64 = 10

type WasmModule struct {
	serviceable commonIface.Serviceable
	ctx         context.Context
	structure   *structureSpec.Function
	branch      string
	commit      string

	// shadows shadows
}

type instanceRequest struct {
	response chan<- instanceRequestResponse
}

type instanceShadow struct {
	creation time.Time
	fI       WasmModule
	runtime  vm.Runtime
	plugin   interface{}
}

type instanceRequestResponse struct {
	instanceShadow
	err error
}

type metricRuntime struct {
	vm.Runtime
	wm *WasmModule
}
