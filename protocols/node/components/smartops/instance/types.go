package instance

import (
	"context"

	iface "github.com/taubyte/go-interfaces/services/substrate/smartops"
	vmCommon "github.com/taubyte/go-interfaces/vm"
	structureSpec "github.com/taubyte/go-specs/structure"
)

var _ iface.Instance = &instance{}

type rtResponse struct {
	runtime       vmCommon.Runtime
	sdkPlugin     interface{}
	smartOpPlugin interface{}
}

type instance struct {
	ctx  context.Context
	ctxC context.CancelFunc

	srv     iface.Service
	context InstanceContext
	util    iface.Util

	path string

	expireOn    uint64
	gracePeriod uint64
	extendTime  chan uint64
	rtRequest   chan chan rtResponse
}

func (i *instance) Context() context.Context {
	return i.ctx
}

func (i *instance) ContextCancel() {
	i.ctxC()
}

type InstanceContext struct {
	Config      structureSpec.SmartOp
	Project     string
	Application string
	Commit      string
}
