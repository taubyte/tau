package function

import (
	"context"

	smartOps "github.com/taubyte/go-interfaces/services/substrate/smartops"
	sdkSmartOpsCommon "github.com/taubyte/go-sdk-smartops/common"
)

var _ smartOps.SmartOpEventCaller = &Function{}

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
