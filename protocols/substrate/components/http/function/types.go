package function

import (
	"context"

	iface "github.com/taubyte/go-interfaces/services/substrate/components/http"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/tau/protocols/substrate/components/http/common"
	"github.com/taubyte/tau/vm"
)

type Function struct {
	srv iface.Service

	config      structureSpec.Function
	matcher     *common.MatchDefinition
	project     string
	application string
	commit      string

	readyDone  bool
	readyCtx   context.Context
	readyCtxC  context.CancelFunc
	readyError error

	instanceCtx  context.Context
	instanceCtxC context.CancelFunc

	dFunc *vm.DFunc
}

func (f *Function) Close() {
	f.instanceCtxC()
}
