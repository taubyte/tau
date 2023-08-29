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
		ctx:             ctx,
		serviceable:     serviceable,
		instanceRequest: make(chan instanceRequest, MaxInstanceRequest),
	}

	initShadow(ctx, &f.shadows)
	f.startInstanceProducer()

	return f
}

func (f *Function) startInstanceProducer() {
	go func() {
		shadows := make(chan instanceShadow, 1000)
		var head *instanceShadow

		for {
			select {
			case <-f.ctx.Done():
				for req := range f.instanceRequest {
					if req.response != nil {
						req.response <- instanceRequestResponse{
							err: f.ctx.Err(),
						}
					}
				}
			case <-time.After(5 * time.Minute):
			case req := <-f.instanceRequest:
				atomic.AddUint64(&f.instanceReqCount, 1)
				res := instanceRequestResponse{}
				if head != nil {
					res.instanceShadow = *head
					select {
					case next := <-shadows:
						head = &next
					default:

						head = nil
					}
				}

				res.fI, res.runtime, res.plugin, res.err = f.instantiate(req.ctx, req.branch, req.commit)
				res.creation = time.Now()

				atomic.AddUint64(&f.runtimeCount, 1)
				req.response <- res
			}
		}
	}()
}
