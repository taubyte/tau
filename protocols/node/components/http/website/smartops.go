package website

import (
	"context"

	smartOps "github.com/taubyte/go-interfaces/services/substrate/smartops"
	sdkSmartOpsCommon "github.com/taubyte/go-sdk-smartops/common"
)

var _ smartOps.SmartOpEventCaller = &Website{}

const resourceType = sdkSmartOpsCommon.ResourceTypeWebsite

func (w *Website) Type() uint32 {
	return uint32(resourceType)
}

func (w *Website) Context() context.Context {
	return w.instanceCtx
}

func (w *Website) Event() interface{} {
	return nil
}

func (w *Website) SmartOps() (uint32, error) {
	return w.srv.SmartOps().Run(w, w.config.SmartOps)
}

func (w *Website) Application() string {
	return w.application
}
