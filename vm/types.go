package vm

import (
	"context"
	"sync/atomic"
	"time"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/go-interfaces/vm"
	structureSpec "github.com/taubyte/go-specs/structure"
)

type Shadows struct {
	ctx       context.Context
	ctxC      context.CancelFunc
	parent    *Function
	instances chan *shadowInstance
	more      chan struct{}
	available atomic.Int64
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

	// gateway metrics
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
