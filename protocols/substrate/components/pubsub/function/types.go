package function

import (
	"context"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/pubsub"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/tau/protocols/substrate/components/pubsub/common"
	"github.com/taubyte/tau/vm"
)

var _ commonIface.Serviceable = &Function{}
var _ iface.Serviceable = &Function{}

type Function struct {
	srv    iface.Service
	config structureSpec.Function

	matcher *common.MatchDefinition
	commit  string

	assetId string

	mmi common.MessagingMapItem

	readyCtx   context.Context
	readyCtxC  context.CancelFunc
	readyError error
	readyDone  bool

	instanceCtx  context.Context
	instanceCtxC context.CancelFunc

	dFunc *vm.DFunc
}

func (f *Function) Close() {
	f.instanceCtxC()
}
