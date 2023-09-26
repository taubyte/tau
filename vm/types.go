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
	errors    atomic.Int64
	available atomic.Int64

	coldStart *Metrics
	calls     *Metrics
}

type Metrics struct {
	totalCount atomic.Int64
	maxMemory  atomic.Int64
	totalTime  atomic.Int64
}

type Function struct {
	serviceable commonIface.Serviceable
	ctx         context.Context
	config      *structureSpec.Function
	branch      string
	commit      string
	vmConfig    *vm.Config
	vmContext   vm.Context

	shadows *Shadows
}

type shadowInstance struct {
	creation  time.Time
	runtime   vm.Runtime
	pluginApi interface{}
}
