package runtime

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/mem"
	"github.com/taubyte/tau/core/vm"
	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
	vmContext "github.com/taubyte/tau/pkg/vm/context"
)

func (i *instance) Free() error {
	fmt.Printf("freeing instance %p\n", i)
	var useMem uint32
	for _, name := range i.runtime.Modules() {
		mod, err := i.runtime.Module(name)
		if err != nil {
			fmt.Printf("error getting module: %v\n", err)
			return err
		}
		useMem += mod.Memory().Size()
	}

	fmt.Printf("used memory: %v\n", useMem)
	if useMem > uint32(i.parent.config.Memory*2/3) {
		fmt.Printf("instance %p used memory is (%d) greater than %d of the memory limit\n", i, useMem, i.parent.config.Memory*2/3)
		return fmt.Errorf("used memory limit exceeded") //TODO: cleanup instance
	}

	fmt.Printf("pushing instance to available instances %p - total calls: %d\n", i, i.parent.calls.Load())
	i.parent.availableInstances <- i
	fmt.Printf("instance %p pushed to available instances - available instances: %d\n", i, len(i.parent.availableInstances))
	return nil
}

func (i *instance) Module(name string) (vm.ModuleInstance, error) {
	return i.runtime.Module(name)
}

func (i *instance) SDK() plugins.Instance {
	return i.sdk
}

func (i *instance) Ready() (Instance, error) {
	// if i.prevMemSize > 0 {
	// 	i.runtime.Module()
	// 	memUsage := i.runtime.Module(i.parent.config.Name).Memory().Size() - i.prevMemSize
	// }
	return i, nil
}

func (i *instance) Close() error {
	return i.runtime.Close()
}

func (i *instance) Stdout() io.Reader {
	return i.runtime.Stdout()
}

func (i *instance) Stderr() io.Reader {
	return i.runtime.Stderr()
}

/*
Instantiate returns a runtime, plugin api, and error
*/
func (f *Function) Instantiate(ctx context.Context) (Instance, error) { //} (runtime vm.Runtime, pluginApi interface{}, err error) {
	instCh := &instanceRequest{ctx: ctx, ch: make(chan Instance, 1)}

	select {
	case f.instanceReqs <- instCh:
	default:
		return nil, fmt.Errorf("instance request channel is full")
	}

	select {
	case <-f.ctx.Done():
		return nil, fmt.Errorf("function context done with: %w", f.ctx.Err())
	case <-ctx.Done():
		return nil, fmt.Errorf("instance request not sent: %w", ctx.Err())
	case instance := <-instCh.ch:
		if instCh.err != nil {
			return nil, instCh.err
		}
		return instance.Ready()
	}
}

func (f *Function) intanceManager() {
	for {
		select {
		case <-f.ctx.Done():
			return
		case reqCh := <-f.instanceReqs:
			fmt.Printf("[func/%s] instance request received - available instances: %d\n", f.config.Name, len(f.availableInstances))
			select {
			case instance := <-f.availableInstances:
				fmt.Printf("[func/%s] instance available %p\n", f.config.Name, instance)
				reqCh.ch <- instance
			default:
				fmt.Printf("[func/%s] instance not available\n", f.config.Name)
				// we need to instantiate a new instance
				// hoever if that does not work we need to repush the request in a way that is it first in line
				fmt.Printf("[func/%s] instantiating new instance\n", f.config.Name)
				runtime, sdk, err := f.instantiate()
				if err == nil {
					fmt.Printf("[func/%s] new instance created\n", f.config.Name)
					reqCh.ch <- &instance{runtime: runtime, sdk: sdk, parent: f}
				} else {
					fmt.Printf("[func/%s] creating new instance failed with: %s\n", f.config.Name, err.Error())
					logger.Errorf("creating new instance failed with: %s", err.Error())
					// we reached some sort of limit
					// wait for an instance to be available
					select {
					case <-reqCh.ctx.Done():
						reqCh.err = fmt.Errorf("instance request context done with: %w", reqCh.ctx.Err())
						reqCh.ch <- nil
					case instance := <-f.availableInstances:
						fmt.Printf("[func/%s] instance available after failed instantiation\n", f.config.Name)
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
func (f *Function) instantiate() (rt vm.Runtime, sdk plugins.Instance, err error) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	goMemory := m.HeapReleased + m.HeapIdle

	// we need to do some resource availability checks
	// get system memory
	if f.config.Memory > goMemory {
		vmStat, err := mem.VirtualMemory()
		if err != nil {
			return nil, nil, fmt.Errorf("getting system memory failed with: %w", err)
		}

		availableMemory := goMemory + (vmStat.Total - vmStat.Used)
		if availableMemory*2/3 < f.config.Memory {
			return nil, nil, fmt.Errorf("insufficient system memory available: %d bytes", availableMemory)
		}
	}

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

	rt, err = instance.Runtime(nil)
	if err != nil {
		err = fmt.Errorf("creating new runtime failed with: %w", err)
		return
	}
	closers = append(closers, rt)

	for _, plugIn := range f.serviceable.Service().Orbitals() {
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
