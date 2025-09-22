package runtime

import (
	"context"
	"sync/atomic"

	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	"github.com/taubyte/tau/core/vm"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"

	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
)

type Function struct {
	serviceable commonIface.Serviceable
	ctx         context.Context
	config      *structureSpec.Function
	branch      string
	commit      string
	vmConfig    *vm.Config
	vmContext   vm.Context

	instanceReqs       chan *instanceRequest
	availableInstances chan Instance

	// metrics
	coldStarts     *atomic.Uint64
	totalColdStart *atomic.Int64

	calls         *atomic.Uint64
	totalCallTime *atomic.Int64
	maxMemory     *atomic.Uint64
}

type instance struct {
	runtime     vm.Runtime
	prevMemSize uint32
	memUsages   []uint32
	sdk         plugins.Instance
	parent      *Function
}

type instanceRequest struct {
	ctx context.Context
	ch  chan Instance
	err error
}

type Instance interface {
	Free() error
	Module(name string) (vm.ModuleInstance, error)
	SDK() plugins.Instance
	Ready() (Instance, error)
	Close() error
}
