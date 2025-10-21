package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/taubyte/tau/core/vm"
)

var DebugFunctionCallsLogger vm.Logger

// Call takes instance and id, then calls the moduled function. Returns an error.
func (f *Function) Call(inst Instance, id uint32) (err error) {
	startTime := time.Now()
	defer func() {
		if err == nil {
			f.calls.Add(1)
			f.totalCallTime.Add(int64(time.Since(startTime)))
		}

		if DebugFunctionCallsLogger != nil {
			logWriter, lgErr := DebugFunctionCallsLogger.New(f.vmContext)
			if lgErr != nil {
				return
			}

			meta := map[string]interface{}{
				"start_time": startTime.UnixNano(),
				"end_time":   time.Now().UnixNano(),
				"duration":   time.Since(startTime).Nanoseconds(),
			}

			if err != nil {
				meta["error"] = err.Error()
			}

			json.NewEncoder(logWriter).Encode(meta)
			io.Copy(logWriter, inst.Stdout())
			io.Copy(logWriter, inst.Stderr())
			logWriter.Close()
		}
	}()

	moduleName, err := f.moduleName()
	if err != nil {
		return fmt.Errorf("getting module name for resource `%s` failed with: %w", f.serviceable.Id(), err)
	}

	module, err := inst.Module(moduleName)
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
		defer func() {
			if internalInst, ok := inst.(*instance); ok {
				f.printRuntimeStack(internalInst.runtime, err)
			}
		}()
	}
	if mem := uint64(module.Memory().Size()); mem > f.maxMemory.Load() {
		f.maxMemory.Store(mem)
	}

	if err != nil {
		return fmt.Errorf("calling function for event %d failed with: %w", id, err)
	}

	return nil
}
