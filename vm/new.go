package tvm

import (
	"context"
	"sync/atomic"

	"github.com/taubyte/go-interfaces/services/substrate"
	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/go-interfaces/vm"
)

type metricRuntime struct {
	vm.Runtime
	f *Function
}

func (mr *metricRuntime) Close() {
	mr.f.closedRuntime()
	mr.Runtime.Close()
}

func (f *Function) closedRuntime() {
	atomic.AddUint64(&f.runtimeClosedCount, 1)
}

func New(ctx context.Context, srv substrate.Service, serviceable commonIface.Serviceable) commonIface.Function {
	f := &Function{
		srv:            srv,
		serviceable:    serviceable,
		intanceRequest: make(chan instanceRequest, MaxIntanceRequest),
	}

	go func() {
		// method
		for {
			select {
			case <-ctx.Done():
				for req := range f.intanceRequest {
					if req.response != nil {
						req.response <- instanceRequestResponse{
							err: ctx.Err(),
						}
					}
				}
			case req := <-f.intanceRequest:
				atomic.AddUint64(&f.instanceReqCount, 1)
				res := instanceRequestResponse{}
				res.fI, res.runtime, res.plugin, res.err = f.instantiate(req.ctx, req.branch, req.commit)
				atomic.AddUint64(&f.runtimeCount, 1)
				req.response <- res
			}
		}
	}()
}
