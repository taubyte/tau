package stream

import (
	"context"
	"fmt"

	iface "github.com/taubyte/go-interfaces/services/substrate/components/p2p"
	sdkSmartOpsCommon "github.com/taubyte/go-sdk-smartops/common"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/odo/protocols/substrate/components/p2p/service"
)

func New(srv iface.Service, ctx context.Context, config *structureSpec.Service, serviceApplication string, matcher *iface.MatchDefinition) (iface.Stream, error) {
	s := &Stream{
		srv:     srv,
		config:  config,
		matcher: matcher,
	}
	s.instanceCtx, s.instanceCtxC = context.WithCancel(ctx)

	if len(config.SmartOps) > 0 {
		_service, err := service.New(ctx, uint32(sdkSmartOpsCommon.ResourceTypeService), srv, matcher.Project, serviceApplication, config)
		if err != nil {
			return nil, err
		}

		val, err := _service.SmartOps(config.SmartOps)
		if err != nil || val > 0 {
			if err != nil {
				return nil, fmt.Errorf("running smart ops failed with: %s", err)
			}
			return nil, fmt.Errorf("exited: %d", val)
		}
	}

	return s, nil
}
