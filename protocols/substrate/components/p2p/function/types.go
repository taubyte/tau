package function

import (
	"context"

	iface "github.com/taubyte/go-interfaces/services/substrate/components/p2p"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/tau/vm"
)

var _ iface.Serviceable = &Function{}

type Function struct {
	srv    iface.Service
	config structureSpec.Function

	serviceConfig      *structureSpec.Service
	serviceApplication string

	assetId string

	matcher *iface.MatchDefinition
	commit  string

	readyCtx   context.Context
	readyCtxC  context.CancelFunc
	readyError error
	readyDone  bool

	instanceCtx  context.Context
	instanceCtxC context.CancelFunc

	*vm.Function
}
