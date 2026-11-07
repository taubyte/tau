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

	// modules maps each wasm module the function loaded (its own, a library
	// it runs as, or one it imports) to the address it resolved to, so a
	// library push can be noticed. nil until the first call has run.
	modulesLock sync.Mutex
	modules     map[string]string
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

	// held: the instance is checked out for a request and counts against its
	// project's caps until it goes back to the pool or is closed.
	held atomic.Bool

	// pooled marks an instance that made it into the pool at least once,
	// distinguishing normal growth-retirement from a config that never pools.
	pooled bool

	// cached function handle, keyed by the module/function name it was
	// resolved under, to avoid a fresh wazy ExportedFunction lookup per call.
	fxModuleName string
	fxName       string
	fxModule     vm.ModuleInstance
	fx           vm.FunctionInstance
}

type instanceRequest struct {
	ctx context.Context
	ch  chan Instance
	err error

	// gone is set once the caller stopped waiting; an instance for it goes back to the pool.
	mu   sync.Mutex
	gone bool
}

// give hands inst to the caller, or back to the pool when the caller has left.
func (r *instanceRequest) give(inst Instance) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gone {
		if inst != nil {
			inst.Free()
		}
		return
	}
	r.ch <- inst
}

// leave marks the caller gone and returns an instance already handed over to the pool.
func (r *instanceRequest) leave() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gone = true
	select {
	case inst := <-r.ch:
		if inst != nil {
			inst.Free()
		}
	default:
	}
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
