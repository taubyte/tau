package tvm

import (
	"context"
	"fmt"
	"sync"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
)

func New(ctx context.Context, serviceable commonIface.Serviceable, branch, commit string) (*WasmModule, error) {
	if structure := serviceable.Structure(); structure != nil {
		w := &WasmModule{
			serviceable: serviceable,
			ctx:         ctx,
			structure:   structure,
			branch:      branch,
			commit:      commit,
		}

		// w.initShadow
		w.initShadow()
		// w.startInstanceProducer()

		return w, nil
	}

	return nil, fmt.Errorf("serviceable `%s` function structure is nil", serviceable.Id())
}

// maybe export check and see
func (w *WasmModule) initShadow() {
	w.shadows = shadows{
		instances: make(chan *instanceShadow, 1024),
		more:      make(chan struct{}),
		parent:    w,
	}

	go func() {
		// defer clean up
		var errCount int
		for {
			select {
			case <-w.ctx.Done():
				return
			case <-w.shadows.more:
				if errCount < 10 {
					var wg sync.WaitGroup
					for i := 0; i < 10; i++ {
						if errCount > 10 {
							break
						}
						wg.Add(1)
						go func() {
							defer wg.Done()
							shadow, err := w.shadows.newInstance()
							if err != nil {
								// log
								errCount++
								return
							}
							w.shadows.instances <- shadow
						}()
					}
					wg.Wait()
				}
			}
		}
	}()
}

// func (f *WasmModule) startInstanceProducer() {
// 	go func() {
// 		shadows := make(chan instanceShadow, 1000)
// 		var head *instanceShadow

// 		for {
// 			select {
// 			case <-f.ctx.Done():
// 				for req := range f.instanceRequest {
// 					if req.response != nil {
// 						req.response <- instanceRequestResponse{
// 							err: f.ctx.Err(),
// 						}
// 					}
// 				}
// 			case <-time.After(5 * time.Minute):
// 			case req := <-f.instanceRequest:
// 				// ALL THIS should now use shadow
// 				atomic.AddUint64(&f.instanceReqCount, 1)
// 				res := instanceRequestResponse{}
// 				if head != nil {
// 					res.instanceShadow = *head
// 					select {
// 					case next := <-shadows:
// 						head = &next
// 					default:

// 						head = nil
// 					}
// 				}

// 				res.fI, res.runtime, res.plugin, res.err = f.instantiate(req.ctx, req.branch, req.commit)
// 				res.creation = time.Now()

// 				req.response <- res
// 			}
// 		}
// 	}()
// }
