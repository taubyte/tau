package vm

import (
	"context"
	"fmt"
	"time"

	"github.com/taubyte/go-interfaces/vm"
)

// Call takes instance and id, then calls the moduled function. Returns an error.
func (f *Function) Call(runtime vm.Runtime, id uint32) (err error) {
	metric := f.shadows.startMetric(f.ctx)
	defer func() {
		if dur, maxAlloc := metric.stop(); err == nil {
			f.shadows.calls.totalCount.Add(1)
			f.shadows.calls.maxMemory.Swap(maxAlloc)
			f.shadows.calls.totalTime.Add(int64(dur))
		}
	}()

	moduleName, err := f.moduleName()
	if err != nil {
		return fmt.Errorf("getting module name for resource `%s` failed with: %w", f.serviceable.Id(), err)
	}

	module, err := runtime.Module(moduleName)
	if err != nil {
		return fmt.Errorf("creating module instance failed with: %w", err)
	}

	fx, err := module.Function(f.config.Call)
	if err != nil {
		return fmt.Errorf("getting wasm function instance failed with: %w", err)
	}

	ctx, ctxC := context.WithTimeout(f.ctx, time.Duration(time.Nanosecond*time.Duration(f.config.Timeout)))
	defer ctxC()

	_, err = fx.RawCall(ctx, uint64(id))
	if f.serviceable.Service().Verbose() {
		defer f.printRuntimeStack(runtime, err)
	}
	if err != nil {
		return fmt.Errorf("calling function for event %d failed with: %w", id, err)
	}

	return nil
}
