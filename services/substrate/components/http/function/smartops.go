package function

import (
	"context"

	sdkSmartOpsCommon "github.com/taubyte/go-sdk-smartops/common"
	"github.com/taubyte/tau/core/services/substrate/smartops"
)

var _ smartops.EventCaller = &Function{}

const resourceType = sdkSmartOpsCommon.ResourceTypeFunctionHTTP

func (f *Function) Type() uint32 {
	return uint32(resourceType)
}

func (f *Function) Context() context.Context {
	return f.instanceCtx
}

func (f *Function) SmartOps() (uint32, error) {
	return f.srv.SmartOps().Run(f, f.config.SmartOps)
}
