package function

import (
	"context"

	"github.com/taubyte/tau/core/services/substrate/components"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/http/common"
	"github.com/taubyte/tau/services/substrate/components/metrics"
	"github.com/taubyte/tau/services/substrate/runtime"
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

	*runtime.Function
}

func (f *Function) Close() {
	f.instanceCtxC()
}
