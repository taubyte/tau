package tvm

import (
	"context"
	"fmt"

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

		// // w.initShadow
		// initShadow(ctx, &w.shadows)
		// w.startInstanceProducer()

		return w, nil
	}

	return nil, fmt.Errorf("serviceable `%s` function structure is nil", serviceable.Id())
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
