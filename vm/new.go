package tvm

import (
	"context"
	"sync/atomic"
	"time"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
)

func (mr *metricRuntime) Close() error {
	mr.f.closedRuntime()
	return mr.Runtime.Close()
}

func (f *Function) closedRuntime() {
	atomic.AddUint64(&f.runtimeClosedCount, 1)
}

func New(ctx context.Context, serviceable commonIface.Serviceable) commonIface.Function {
	f := &Function{
		serviceable:     serviceable,
		instanceRequest: make(chan instanceRequest, MaxInstanceRequest),
	}

	go func() {
		// method
		for {
			select {
			case <-ctx.Done():
				for req := range f.instanceRequest {
					if req.response != nil {
						req.response <- instanceRequestResponse{
							err: ctx.Err(),
						}
					}
				}
			case req := <-f.instanceRequest:
				atomic.AddUint64(&f.instanceReqCount, 1)
				res := instanceRequestResponse{}
				res.fI, res.runtime, res.plugin, res.err = f.instantiate(req.ctx, req.branch, req.commit)
				go func() {
					<-time.After(ShadowTTL)
					res.runtime.Close()
				}()
				atomic.AddUint64(&f.runtimeCount, 1)
				req.response <- res
			}
		}
	}()

	return f
}
