package tvm

import (
	"context"
	"sync"
	"time"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/go-interfaces/vm"
	structureSpec "github.com/taubyte/go-specs/structure"
)

var MaxInstanceRequest = 1024 * 64
var ShadowBuff uint64 = 10

type shadows struct {
	parent    *WasmModule
	instances chan *instanceShadow
	gcLock    sync.RWMutex

	more chan struct{}
}

type WasmModule struct {
	serviceable commonIface.Serviceable
	ctx         context.Context
	structure   *structureSpec.Function
	branch      string
	commit      string

	shadows shadows
}

type instanceShadow struct {
	creation  time.Time
	runtime   vm.Runtime
	pluginApi interface{}
}

// Might just remove this for now, not really using this functionality
type metricRuntime struct {
	vm.Runtime
	wm *WasmModule
}

// type instanceRequest struct {
// 	response chan<- instanceRequestResponse
// }

// type instanceRequestResponse struct {
// 	instanceShadow
// 	err error
// }
