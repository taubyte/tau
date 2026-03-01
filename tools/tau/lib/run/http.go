package run

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/taubyte/tau/core/vm"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/pkg/vm/backend/file"
	vmContext "github.com/taubyte/tau/pkg/vm/context"
	loader "github.com/taubyte/tau/pkg/vm/loaders/wazero"
	fileRes "github.com/taubyte/tau/pkg/vm/resolvers/file"
	vmWaz "github.com/taubyte/tau/pkg/vm/service/wazero"
	source "github.com/taubyte/tau/pkg/vm/sources/taubyte"
)

// HttpFunction runs an HTTP function by loading the WASM from wasmPath, creating a synthetic HTTP event,
// and calling the function's export. The response is written to w.
func HttpFunction(ctx context.Context, wasmPath string, fnSpec *structureSpec.Function, project, application string, req *http.Request, w http.ResponseWriter) error {
	resolver := fileRes.New(wasmPath)
	ldr := loader.New(resolver, file.New())
	src := source.New(ldr)
	svc := vmWaz.New(ctx, src)

	opts := []vmContext.Option{
		vmContext.Project(project),
		vmContext.Resource(fnSpec.Id),
	}
	if application != "" {
		opts = append(opts, vmContext.Application(application))
	}
	vmCtx, err := vmContext.New(ctx, opts...)
	if err != nil {
		return fmt.Errorf("creating vm context: %w", err)
	}

	memPages := memoryLimitPages(fnSpec.Memory)
	instance, err := svc.New(vmCtx, vm.Config{
		MemoryLimitPages: memPages,
		Output:           vm.Stdio,
	})
	if err != nil {
		return fmt.Errorf("creating vm instance: %w", err)
	}
	defer instance.Close()

	rt, err := instance.Runtime(nil)
	if err != nil {
		return fmt.Errorf("creating runtime: %w", err)
	}
	defer rt.Close()

	pi, _, err := rt.Attach(&minimalPlugin{})
	if err != nil {
		return fmt.Errorf("attaching plugin: %w", err)
	}
	defer pi.Close()

	minPi, ok := pi.(*minimalPluginInstance)
	if !ok {
		return fmt.Errorf("plugin instance type mismatch")
	}

	ev := minPi.eventFactory.CreateHttpEvent(w, req)

	moduleName := fnSpec.ModuleName()
	module, err := rt.Module(moduleName)
	if err != nil {
		return fmt.Errorf("loading module %q: %w", moduleName, err)
	}

	fn, err := module.Function(fnSpec.Call)
	if err != nil {
		return fmt.Errorf("getting function %q: %w", fnSpec.Call, err)
	}

	timeout := time.Duration(fnSpec.Timeout) * time.Nanosecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	_, err = fn.RawCall(callCtx, uint64(ev.Id))
	if err != nil {
		return fmt.Errorf("calling function: %w", err)
	}
	return nil
}

func memoryLimitPages(memoryBytes uint64) uint32 {
	pageSize := uint64(vm.MemoryPageSize)
	limit := uint64(vm.MemoryLimitPages)
	pages := memoryBytes / pageSize
	if memoryBytes%pageSize != 0 {
		pages++
	}
	if pages > limit {
		pages = limit
	}
	return uint32(pages)
}
