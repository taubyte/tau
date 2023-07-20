package function

import (
	"context"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/pubsub"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/odo/protocols/node/components/pubsub/common"
)

var _ commonIface.Serviceable = &Function{}
var _ iface.Serviceable = &Function{}

type Function struct {
	srv      iface.Service
	config   structureSpec.Function
	function commonIface.Function

	matcher *common.MatchDefinition
	commit  string

	mmi common.MessagingMapItem

	readyCtx   context.Context
	readyCtxC  context.CancelFunc
	readyError error
	readyDone  bool

	instanceCtx  context.Context
	instanceCtxC context.CancelFunc
}

func (f *Function) Close() {
	f.instanceCtxC()
}
