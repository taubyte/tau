package stream

import (
	"context"

	"github.com/ipfs/go-cid"
	smartOps "github.com/taubyte/go-interfaces/services/substrate/smartops"
	sdkSmartOpsCommon "github.com/taubyte/go-sdk-smartops/common"
)

var _ smartOps.SmartOpEventCaller = &Stream{}

const resourceType = sdkSmartOpsCommon.ResourceTypeService

func (f *Stream) Type() uint32 {
	return uint32(resourceType)
}

func (f *Stream) Context() context.Context {
	return f.instanceCtx
}

func (f *Stream) Event() interface{} {
	return nil
}

func (f *Stream) SmartOps() (uint32, error) {
	return f.srv.SmartOps().Run(f, f.config.SmartOps)
}

func (f *Stream) Application() string {
	return f.matcher.Application
}

func (f *Stream) Project() (cid.Cid, error) {
	return cid.Decode(f.matcher.Project)
}
