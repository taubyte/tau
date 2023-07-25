package function

import (
	"context"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/p2p"
	structureSpec "github.com/taubyte/go-specs/structure"
)

var _ iface.Serviceable = &Function{}

type Function struct {
	srv    iface.Service
	config structureSpec.Function

	serviceConfig      *structureSpec.Service
	serviceApplication string

	function commonIface.Function

	matcher *iface.MatchDefinition
	commit  string

	readyCtx   context.Context
	readyCtxC  context.CancelFunc
	readyError error
	readyDone  bool

	instanceCtx  context.Context
	instanceCtxC context.CancelFunc
}
