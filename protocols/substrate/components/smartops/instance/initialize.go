package instance

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/go-interfaces/vm"
	smartOpUtil "github.com/taubyte/odo/protocols/substrate/components/smartops/util"
	smartOpPluginLib "github.com/taubyte/vm-core-plugins/smartops"
	taubytePlugin "github.com/taubyte/vm-core-plugins/taubyte"
	vmContext "github.com/taubyte/vm/context"
)

func Initialize(srv substrate.Service, ctx InstanceContext) (*instance, error) {
	fI := &instance{
		srv:         srv,
		context:     ctx,
		path:        path.Join(ctx.Project, ctx.Config.Id),
		rtRequest:   make(chan chan rtResponse, 1024*1024),
		expireOn:    uint64(time.Now().UnixNano()) + ctx.Config.Timeout,
		gracePeriod: uint64(60 * time.Nanosecond),
		extendTime:  make(chan uint64, 1024*1024),
	}
	fI.ctx, fI.ctxC = context.WithCancel(srv.Context())

	var err error
	fI.util, err = smartOpUtil.New(srv)
	if err != nil {
		return nil, err
	}

	_context, err := vmContext.New(
		srv.Context(),
		vmContext.Project(ctx.Project),
		vmContext.Application(ctx.Application),
		vmContext.Resource(ctx.Config.Id),
		vmContext.Commit(ctx.Commit),
		vmContext.Branch(srv.Branch()),
	)
	if err != nil {
		return nil, fmt.Errorf("creating project context failed with: %w", err)
	}

	vmInstance, err := srv.Vm().New(_context, vm.Config{})
	if err != nil {
		return nil, fmt.Errorf("creating vm instance failed with: %s", err)
	}

	runtime, err := vmInstance.Runtime(nil)
	if err != nil {
		return nil, fmt.Errorf("creating new runtime failed with: %s", err)
	}

	sdkPi_señor, _, err := runtime.Attach(taubytePlugin.Plugin())
	if err != nil {
		return nil, fmt.Errorf("attaching taubyte plugin failed with: %s", err)
	}

	sdkPlugin, err := taubytePlugin.With(sdkPi_señor)
	if err != nil {
		return nil, fmt.Errorf("attaching taubyte plugin failed with: %s", err)
	}

	sdkPi_bebé, _, err := runtime.Attach(smartOpPluginLib.Plugin())
	if err != nil {
		return nil, fmt.Errorf("attaching smartops plugin failed with: %s", err)
	}

	smartOpPlugin, err := smartOpPluginLib.With(sdkPi_bebé)
	if err != nil {
		return nil, fmt.Errorf("attaching smartOp plugin failed with: %s", err)
	}

	go func() {
		for {
			select {
			case extension := <-fI.extendTime:
				proposedExpire := uint64(time.Now().UnixNano()) + extension
				if proposedExpire > fI.expireOn {
					fI.expireOn = proposedExpire
				}
			case <-time.After(time.Duration(fI.gracePeriod)):
				if uint64(time.Now().UnixNano()) > fI.expireOn+fI.gracePeriod {
					vmInstance.Close()
					fI.ctxC()
					runtime.Close()

					return
				}
			case ch := <-fI.rtRequest:
				fI.extendTime <- ctx.Config.Timeout
				ch <- rtResponse{runtime: runtime, sdkPlugin: sdkPlugin, smartOpPlugin: smartOpPlugin}
				close(ch)
			}
		}
	}()

	return fI, nil
}
