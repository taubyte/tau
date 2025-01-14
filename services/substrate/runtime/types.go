package runtime

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	"github.com/taubyte/tau/core/vm"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

type Shadows struct {
	ctx          context.Context
	ctxC         context.CancelFunc
	parent       *Function
	instances    chan *shadowInstance
	more         chan int
	available    atomic.Int64
	requestCount int64      // total number of requests
	lastCheck    time.Time  // last time we calculated RPS
	currentRPS   float64    // current requests per second
	mu           sync.Mutex // mutex for RPS calculations
}

type Function struct {
	serviceable commonIface.Serviceable
	ctx         context.Context
	config      *structureSpec.Function
	branch      string
	commit      string
	vmConfig    *vm.Config
	vmContext   vm.Context

	shadows    *Shadows
	errorCount atomic.Int64

	// metrics
	coldStarts     *atomic.Uint64
	totalColdStart *atomic.Int64

	calls         *atomic.Uint64
	totalCallTime *atomic.Int64
	maxMemory     *atomic.Uint64
}

type shadowInstance struct {
	creation  time.Time
	runtime   vm.Runtime
	pluginApi interface{}
}
