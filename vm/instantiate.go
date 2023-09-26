package vm

import (
	"fmt"
	"io"

	"github.com/taubyte/go-interfaces/vm"
	plugins "github.com/taubyte/vm-core-plugins/taubyte"
	vmContext "github.com/taubyte/vm/context"
)

/*
Instantiate returns a runtime, plugin api, and error
*/
func (f *Function) Instantiate() (runtime vm.Runtime, pluginApi interface{}, err error) {
	shadow, err := f.shadows.get()
	if err != nil {
		return nil, nil, err
	}

	return shadow.runtime, shadow.pluginApi, nil
}

/*
instantiate method initializes the wasm runtime and attaches plugins.
Returns the runtime, plugin api, and error
*/
func (f *Function) instantiate() (runtime vm.Runtime, pluginApi interface{}, err error) {
	metric := f.shadows.startMetric(f.ctx)
	defer func() {
		if dur, maxAlloc := metric.stop(); err == nil {
			f.shadows.coldStart.totalCount.Add(1)
			f.shadows.coldStart.maxMemory.Swap(maxAlloc)
			f.shadows.coldStart.totalTime.Add(int64(dur))
		}
	}()

	if f.vmContext == nil {
		f.vmContext, err = vmContext.New(
			f.ctx,
			vmContext.Project(f.serviceable.Project()),
			vmContext.Application(f.serviceable.Application()),
			vmContext.Resource(f.serviceable.Id()),
			vmContext.Commit(f.commit),
			vmContext.Branch(f.branch),
		)
		if err != nil {
			err = fmt.Errorf("creating vm context failed with: %w", err)
			return
		}
	}

	if f.vmConfig == nil {
		f.vmConfig = &vm.Config{
			MemoryLimitPages: uint32(
				roundedUpDivWithUpperLimit(
					f.config.Memory,
					uint64(vm.MemoryPageSize),
					uint64(vm.MemoryLimitPages),
				),
			),
		}
	}

	if f.serviceable.Service().Verbose() {
		f.vmConfig.Output = vm.Buffer
	}

	instance, err := f.serviceable.Service().Vm().New(f.vmContext, *f.vmConfig)
	if err != nil {
		err = fmt.Errorf("creating new instance failed with: %w", err)
		return
	}

	var toCloseIfErr []io.Closer
	defer func() {
		if err != nil {
			for _, toClose := range toCloseIfErr {
				toClose.Close()
			}
		}
	}()
	toCloseIfErr = append(toCloseIfErr, instance)

	runtime, err = instance.Runtime(nil)
	if err != nil {
		err = fmt.Errorf("creating new runtime failed with: %w", err)
		return
	}
	toCloseIfErr = append(toCloseIfErr, runtime)

	for _, plugIn := range f.serviceable.Service().Orbitals() {
		if _, _, err = runtime.Attach(plugIn); err != nil {
			err = fmt.Errorf("attaching satellite plugin `%s` to runtime failed with: %w", plugIn.Name(), err)
			return
		}
	}

	sdkPi, _, err := runtime.Attach(plugins.Plugin())
	if err != nil {
		err = fmt.Errorf("attaching core plugins to runtime failed with: %w", err)
		return
	}

	if pluginApi, err = plugins.With(sdkPi); err != nil {
		err = fmt.Errorf("loading plugin api failed with: %w", err)
		return
	}

	return
}
