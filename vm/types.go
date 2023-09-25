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
	count     atomic.Int64

	more chan struct{}
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
