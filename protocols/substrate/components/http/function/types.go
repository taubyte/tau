package function

import (
	"context"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/http"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/odo/protocols/substrate/components/http/common"
)

var _ iface.Serviceable = &Function{}
var _ iface.Function = &Function{}

type Function struct {
	srv iface.Service

	config      structureSpec.Function
	matcher     *common.MatchDefinition
	project     string
	application string
	commit      string

	function commonIface.Function

	readyDone  bool
	readyCtx   context.Context
	readyCtxC  context.CancelFunc
	readyError error

	instanceCtx  context.Context
	instanceCtxC context.CancelFunc
}

func (f *Function) Close() {
	f.instanceCtxC()
}
