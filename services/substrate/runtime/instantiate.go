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

func (i *instance) usedMemory() (uint32, error) {
	var useMem uint32
	for _, name := range i.runtime.Modules() {
		mod, err := i.runtime.Module(name)
		if err != nil {
			return 0, err
		}
		useMem += mod.Memory().Size()
	}

	return useMem, nil
}

// shouldRetire decides whether a pooled instance should be discarded instead
// of reused. wasm linear memory only ever grows, so once usage crosses the
// last third of the enforced cap we retire the instance while it can still be
// replaced cheaply, rather than letting its allocator hit the cap mid-call
// later. The threshold is measured against the page-derived cap that is
// actually enforced, not raw config.Memory: pages round up, so the byte value
// understates the real cap and would retire instances that still have
// headroom. Modules whose minimum footprint already sits past two thirds of
// the cap never pool — with no room to grow, reuse only defers a mid-call OOM
// trap, so cold-starting every call is the correct behavior for such
// under-provisioned functions.
func shouldRetire(useMem uint32, capBytes uint64) bool {
	return uint64(useMem) > capBytes*2/3
}

func (i *instance) Free() error {
	if i.failed {
		i.Close()
		return nil
	}

	useMem, err := i.usedMemory()
	if err != nil {
		i.Close()
		return err
	}

	capBytes := uint64(i.parent.vmConfig.MemoryLimitPages) * uint64(vm.MemoryPageSize)
	if shouldRetire(useMem, capBytes) {
		i.Close()
		return nil
	}

	i.parent.availableInstances <- i
	return nil
}

func (i *instance) Module(name string) (vm.ModuleInstance, error) {
	return i.runtime.Module(name)
}

// function returns cached module/function handles for moduleName/fxName,
// resolving and caching them on first use. Instances are used by one request
// at a time, so no locking is required.
func (i *instance) function(moduleName, fxName string) (vm.ModuleInstance, vm.FunctionInstance, error) {
	if i.fxModule != nil && i.fxModuleName == moduleName && i.fxName == fxName {
		return i.fxModule, i.fx, nil
	}

	mod, err := i.runtime.Module(moduleName)
	if err != nil {
		return nil, nil, err
	}

	fx, err := mod.Function(fxName)
	if err != nil {
		return nil, nil, err
	}

	i.fxModuleName = moduleName
	i.fxName = fxName
	i.fxModule = mod
	i.fx = fx

	return mod, fx, nil
}

func (i *instance) SDK() plugins.Instance {
	return i.sdk
}

func (i *instance) Ready() (Instance, error) {
	// TODO: track memory usage of each call so we can estimate of future calls - lowering the chance of a future call failing and eliminating the static 2/3 memory limit
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
	// Check if shutdown is in progress
	if f.shutdown.Load() {
		return nil, fmt.Errorf("function is shutting down")
	}

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
	defer close(f.shutdownDone)

	for {
		select {
		case <-f.ctx.Done():
			return
		case reqCh, ok := <-f.instanceReqs:
			if !ok {
				// instanceReqs channel is closed, shutdown initiated
				// Process any remaining requests in the channel
				for reqCh := range f.instanceReqs {
					f.processRequest(reqCh)
				}
				return
			}
			f.processRequest(reqCh)
		}
	}
}

func (f *Function) processRequest(reqCh *instanceRequest) {
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
				reqCh.err = fmt.Errorf("instance request context done with: %w", reqCh.ctx.Err())
				reqCh.ch <- nil
			case instance := <-f.availableInstances:
				reqCh.ch <- instance
			case <-f.ctx.Done():
				return
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
