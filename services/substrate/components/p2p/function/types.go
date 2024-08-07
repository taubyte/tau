package function

import (
	"context"

	iface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/runtime"
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
	branch  string

	readyCtx   context.Context
	readyCtxC  context.CancelFunc
	readyError error
	readyDone  bool

	instanceCtx  context.Context
	instanceCtxC context.CancelFunc

	*runtime.Function
}
