package function

import (
	"context"
	"sync"

	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	iface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
	"github.com/taubyte/tau/services/substrate/runtime"
)

var _ commonIface.Serviceable = &Function{}
var _ iface.Serviceable = &Function{}

type Function struct {
	srv    iface.Service
	config structureSpec.Function

	matcher *common.MatchDefinition
	commit  string
	branch  string

	assetId string

	mmi common.MessagingMapItem

	readyCtx   context.Context
	readyCtxC  context.CancelFunc
	readyError error
	readyDone  bool

	instanceCtx  context.Context
	instanceCtxC context.CancelFunc

	closeOnce sync.Once

	*runtime.Function
}

func (f *Function) Close() {
	f.closeOnce.Do(func() {
		f.close()
	})
}

func (f *Function) close() {
	go func() {
		f.Shutdown()
		f.instanceCtxC()
	}()
}
