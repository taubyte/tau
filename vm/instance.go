package tvm

import (
	"fmt"
	"path"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/go-interfaces/vm"
	plugins "github.com/taubyte/vm-core-plugins/taubyte"
	vmContext "github.com/taubyte/vm/context"
)

func roundedUpDivWithUpperLimit(val, chunkSize, limit uint64) uint64 {
	count := val / chunkSize
	if val%chunkSize != 0 {
		count++
	}
	if count > limit {
		count = limit
	}

	return count
}

// Instantiate method returns a Function instance with channels for getting a runtime, and plugin.
func (f *Function) Instantiate(ctx commonIface.FunctionContext, branch, commit string) (commonIface.FunctionInstance, vm.Runtime, interface{}, error) {
	fI := &FunctionInstance{
		project:     ctx.Project,
		application: ctx.Application,
		config:      ctx.Config,
		parent:      f,
		path:        path.Join(ctx.Project, ctx.Config.Id),
	}

	_context, err := vmContext.New(
		f.srv.Context(),
		vmContext.Project(ctx.Project),
		vmContext.Application(ctx.Application),
		vmContext.Resource(fI.config.Id),
		vmContext.Commit(commit),
		vmContext.Branch(branch),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating project context for project `%s` failed with: %s", ctx.Project, err)
	}

	config := vm.Config{
		MemoryLimitPages: uint32(
			roundedUpDivWithUpperLimit(
				fI.config.Memory,
				uint64(vm.MemoryPageSize),
				uint64(vm.MemoryLimitPages),
			)),
	}

	if f.srv.Verbose() {
		config.Output = vm.Buffer
	}

	instance, err := f.srv.Vm().New(_context, config)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating new vm instance failed with: %s", err)
	}

	runtime, err := instance.Runtime(nil)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating new runtime failed with: %s", err)

	}

	for _, plug := range f.srv.Orbitals() {
		if _, _, err = runtime.Attach(plug); err != nil {
			return nil, nil, nil, fmt.Errorf("attaching plugin %s failed with: %w", plug.Name(), err)
		}
	}

	sdkPi, _, err := runtime.Attach(plugins.Plugin())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating plugin instance failed with: %s", err)
	}

	plugin, err := plugins.With(sdkPi)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("attaching plugins failed with: %s", err)
	}

	return fI, runtime, plugin, nil
}
