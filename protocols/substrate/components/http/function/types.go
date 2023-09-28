package function

import (
	"context"

	"github.com/taubyte/go-interfaces/services/substrate/components"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/tau/protocols/substrate/components/http/common"
	"github.com/taubyte/tau/protocols/substrate/components/metrics"
	"github.com/taubyte/tau/vm"
)

type Function struct {
	srv components.ServiceComponent

	config      structureSpec.Function
	matcher     *common.MatchDefinition
	project     string
	application string
	commit      string
	branch      string

	assetId string

	readyDone  bool
	readyCtx   context.Context
	readyCtxC  context.CancelFunc
	readyError error

	provisioned bool

	instanceCtx  context.Context
	instanceCtxC context.CancelFunc

	metrics metrics.Function

	*vm.Function
}

func (f *Function) Close() {
	f.instanceCtxC()
}
