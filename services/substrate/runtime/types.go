package runtime

import (
	"context"
	"io"
	"sync"
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

	// shutdown tracking
	shutdown     *atomic.Bool
	shutdownDone chan struct{}
	shutdownMu   sync.RWMutex

	// metrics
	coldStarts     *atomic.Uint64
	totalColdStart *atomic.Int64

	calls         *atomic.Uint64
	totalCallTime *atomic.Int64
	maxMemory     *atomic.Uint64

	// noPoolWarned gates the once-per-function warning emitted when instances
	// retire without ever pooling (memory config leaves no headroom).
	noPoolWarned atomic.Bool
}

type instance struct {
	runtime     vm.Runtime
	prevMemSize uint32
	memUsages   []uint32
	sdk         plugins.Instance
	parent      *Function

	// failed marks an instance whose call errored or timed out; its runtime
	// is in an unknown (possibly closed) state, so Free retires it instead of
	// repooling.
	failed bool

	// pooled marks an instance that made it into the pool at least once,
	// distinguishing normal growth-retirement from a config that never pools.
	pooled bool

	// cached function handle, keyed by the module/function name it was
	// resolved under, to avoid a fresh wazero ExportedFunction lookup per call.
	fxModuleName string
	fxName       string
	fxModule     vm.ModuleInstance
	fx           vm.FunctionInstance
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
	Stdout() io.Reader
	Stderr() io.Reader
	Ready() (Instance, error)
	Close() error
}
