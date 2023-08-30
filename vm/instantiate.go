package tvm

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
func (w *WasmModule) Instantiate() (runtime vm.Runtime, pluginApi interface{}, err error) {
	shadow, err := w.shadows.get()
	if err != nil {
		return nil, nil, err
	}

	return shadow.runtime, shadow.pluginApi, nil
}

/*
instantiate method initializes the wasm runtime and attaches plugins.
Returns the runtime, plugin api, and error
*/
func (w *WasmModule) instantiate() (runtime *metricRuntime, pluginApi interface{}, err error) {
	serviceable := w.serviceable
	context, err := vmContext.New(
		w.ctx,
		vmContext.Project(serviceable.Project()),
		vmContext.Application(serviceable.Application()),
		vmContext.Resource(serviceable.Id()),
		vmContext.Commit(w.commit),
		vmContext.Branch(w.branch),
	)
	if err != nil {
		err = fmt.Errorf("creating vm context failed with: %w", err)
		return
	}

	config := vm.Config{
		MemoryLimitPages: uint32(
			roundedUpDivWithUpperLimit(
				w.structure.Memory,
				uint64(vm.MemoryPageSize),
				uint64(vm.MemoryLimitPages),
			),
		),
	}

	if serviceable.Service().Verbose() {
		config.Output = vm.Buffer
	}

	instance, err := serviceable.Service().Vm().New(context, config)
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

	rt, err := instance.Runtime(nil)
	if err != nil {
		err = fmt.Errorf("creating new runtime failed with: %w", err)
		return
	}
	toCloseIfErr = append(toCloseIfErr, rt)

	for _, plugIn := range serviceable.Service().Orbitals() {
		if _, _, err = rt.Attach(plugIn); err != nil {
			err = fmt.Errorf("attaching satellite plugin `%s` to runtime failed with: %w", plugIn.Name(), err)
			return
		}
	}

	sdkPi, _, err := rt.Attach(plugins.Plugin())
	if err != nil {
		err = fmt.Errorf("attaching core plugins to runtime failed with: %w", err)
		return
	}

	if pluginApi, err = plugins.With(sdkPi); err != nil {
		err = fmt.Errorf("loading plugin api failed with: %w", err)
		return
	}

	runtime = &metricRuntime{Runtime: rt, wm: w}

	return
}
