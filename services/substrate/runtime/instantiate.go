package runtime

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/taubyte/tau/core/vm"
	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
	vmContext "github.com/taubyte/tau/pkg/vm/context"
)

func (i *instance) Free() error {
	i.parent.availableInstances <- i
	return nil
}

func (i *instance) Module(name string) (vm.ModuleInstance, error) {
	return i.runtime.Module(name)
}

func (i *instance) SDK() plugins.Instance {
	return i.sdk
}

/*
Instantiate returns a runtime, plugin api, and error
*/
func (f *Function) Instantiate(ctx context.Context) (Instance, error) { //} (runtime vm.Runtime, pluginApi interface{}, err error) {
	instCh := &instanceRequest{ch: make(chan Instance, 1)}
	f.instanceReqs <- instCh

	select {
	case <-f.ctx.Done():
		return nil, f.ctx.Err()
	case <-ctx.Done():
		return nil, ctx.Err()
	case instance := <-instCh.ch:
		return instance, instCh.err
	}
}

func (f *Function) intanceManager() {
	for {
		select {
		case <-f.ctx.Done():
			return
		case reqCh := <-f.instanceReqs:
			select {
			case instance := <-f.availableInstances:
				reqCh.ch <- instance
			default:
				// we need to instantiate a new instance
				// hoever if that does not work we need to repush the request in a way that is it first in line
				runtime, sdk, err := f.instantiate()
				if err == nil {
					reqCh.ch <- &instance{runtime: runtime, sdk: sdk, parent: f}
				} else {
					logger.Errorf("creating new instance failed with: %s", err.Error())
					// we reached some sort of limit
					// wait for an instance to be available
					select {
					case <-reqCh.ctx.Done():
						reqCh.err = reqCh.ctx.Err()
						reqCh.ch <- nil
					case instance := <-f.availableInstances:
						reqCh.ch <- instance
					case <-f.ctx.Done():
						return
					}
				}
			}
		}
	}
}

/*
instantiate method initializes the wasm runtime and attaches plugins.
Returns the runtime, plugin api, and error
*/
func (f *Function) instantiate() (runtime vm.Runtime, sdk plugins.Instance, err error) {
	// add cold start metrics if instantiate is successful
	start := time.Now()
	defer func() {
		if err == nil {
			f.coldStarts.Add(1)
			f.totalColdStart.Add(int64(time.Since(start)))
		}
	}()

	// sets vmContext and vmConfig if not already set
	if err = f.configureVM(); err != nil {
		return
	}

	// create vm instance
	instance, err := f.serviceable.Service().Vm().New(f.vmContext, *f.vmConfig)
	if err != nil {
		err = fmt.Errorf("creating new instance failed with: %w", err)
		return
	}

	// if error, make sure to close all
	var closers []io.Closer
	defer func() {
		if err != nil {
			for _, toClose := range closers {
				toClose.Close()
			}
		}
	}()
	closers = append(closers, instance)

	runtime, err = instance.Runtime(nil)
	if err != nil {
		err = fmt.Errorf("creating new runtime failed with: %w", err)
		return
	}
	closers = append(closers, runtime)

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
	closers = append(closers, sdkPi)

	if sdk, err = plugins.With(sdkPi); err != nil {
		err = fmt.Errorf("loading plugin api failed with: %w", err)
		return
	}

	return
}

func (f *Function) configureVM() error {
	if f.vmContext == nil {
		var err error
		f.vmContext, err = vmContext.New(
			f.ctx,
			vmContext.Project(f.serviceable.Project()),
			vmContext.Application(f.serviceable.Application()),
			vmContext.Resource(f.serviceable.Id()),
			vmContext.Commit(f.commit),
			vmContext.Branch(f.branch),
		)
		if err != nil {
			return fmt.Errorf("creating vm context failed with: %w", err)
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

		if f.serviceable.Service().Verbose() {
			f.vmConfig.Output = vm.Buffer
		}
	}

	return nil
}
