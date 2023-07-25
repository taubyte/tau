package function

import (
	"context"

	"github.com/taubyte/go-interfaces/services/substrate"
	sdkSmartOpsCommon "github.com/taubyte/go-sdk-smartops/common"
	"github.com/taubyte/odo/protocols/substrate/components/pubsub/messaging"
	"github.com/taubyte/vm-core-plugins/taubyte/event"
)

var _ substrate.SmartOpEventCaller = &Function{}

func (f *Function) Type() uint32 {
	return uint32(sdkSmartOpsCommon.ResourceTypeFunctionPubSub)
}

func (f *Function) Context() context.Context {
	return f.instanceCtx
}

func (f *Function) SmartOps(ev *event.Event) (uint32, error) {
	// Run smartOps for the matched channel(s)
	for _, ch := range f.mmi.Items {
		smartOps := ch.Config().SmartOps
		if len(smartOps) > 0 {
			m, err := messaging.New(
				f.Context(),
				ev,
				uint32(sdkSmartOpsCommon.ResourceTypeMessagingPubSub),
				f.srv,
				ch,
			)

			// TODO, should not error here, should remove from the mmi, then continue running the function with
			// the reduced mmi
			if err != nil {
				return 0, err
			}

			val, err := m.SmartOps(smartOps)
			if err != nil || val > 0 {
				return val, err
			}
		}
	}

	// Run smartOps for the connected function
	return f.srv.SmartOps().Run(f, f.config.SmartOps)
}

func (f *Function) Application() string {
	return f.matcher.Application
}
