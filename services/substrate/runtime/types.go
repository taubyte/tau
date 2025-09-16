package runtime

import (
	"context"
	"sync/atomic"
	"time"

	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	"github.com/taubyte/tau/core/vm"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"

	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
)

// type shadows struct {
// 	ctx          context.Context
// 	ctxC         context.CancelFunc
// 	parent       *Function
// 	instances    chan *shadowInstance
// 	more         chan int
// 	available    atomic.Int64
// 	requestCount int64      // total number of requests
// 	lastCheck    time.Time  // last time we calculated RPS
// 	currentRPS   float64    // current requests per second
// 	mu           sync.Mutex // mutex for RPS calculations
// }

type Function struct {
	serviceable commonIface.Serviceable
	ctx         context.Context
	config      *structureSpec.Function
	branch      string
	commit      string
	vmConfig    *vm.Config
	vmContext   vm.Context

	// shadows    *shadows
	// errorCount atomic.Int64

	instanceReqs       chan *instanceRequest
	availableInstances chan Instance

	// metrics
	coldStarts     *atomic.Uint64
	totalColdStart *atomic.Int64

	calls         *atomic.Uint64
	totalCallTime *atomic.Int64
	maxMemory     *atomic.Uint64
}

// type shadowInstance struct {
// 	creation  time.Time
// 	runtime   vm.Runtime
// 	pluginApi interface{}
// }

type instance struct {
	creation    time.Time
	runtime     vm.Runtime
	prevMemSize uint32
	memUsages   []uint32
	sdk         plugins.Instance // interface{}
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
}
