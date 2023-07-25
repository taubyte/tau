package function

import (
	"context"

	"github.com/taubyte/go-interfaces/services/substrate"
	sdkSmartOpsCommon "github.com/taubyte/go-sdk-smartops/common"
	"github.com/taubyte/odo/protocols/substrate/components/p2p/service"
)

var _ substrate.SmartOpEventCaller = &Function{}

const resourceType = sdkSmartOpsCommon.ResourceTypeFunctionP2P

func (f *Function) Type() uint32 {
	return uint32(resourceType)
}

func (f *Function) Context() context.Context {
	return f.instanceCtx
}

func (f *Function) SmartOps() (uint32, error) {
	// Run smartOps for the matched services(s)
	if len(f.serviceConfig.SmartOps) > 0 {
		s, err := service.New(
			f.Context(),
			uint32(sdkSmartOpsCommon.ResourceTypeService),
			f.srv,
			f.matcher.Project,
			f.serviceApplication,
			f.serviceConfig,
		)
		if err != nil {
			return 0, err
		}

		val, err := s.SmartOps(f.serviceConfig.SmartOps)
		if err != nil || val > 0 {
			return val, err
		}
	}

	return f.srv.SmartOps().Run(f, f.config.SmartOps)
}
