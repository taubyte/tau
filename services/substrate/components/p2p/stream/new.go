package stream

import (
	"context"
	"fmt"

	sdkSmartOpsCommon "github.com/taubyte/go-sdk-smartops/common"
	iface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	"github.com/taubyte/tau/p2p/streams/client"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/p2p/service"
)

func New(srv iface.Service, ctx context.Context, config *structureSpec.Service, serviceApplication string, matcher *iface.MatchDefinition, p2pClient *client.Client) (iface.Stream, error) {
	s := &Stream{
		srv:     srv,
		config:  config,
		matcher: matcher,
		client:  p2pClient,
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
