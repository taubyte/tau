package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/v4/mem"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/platformerr"
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
// wasm memory never shrinks, so cap what a function's idle pool may hold
// instead of letting 64 slots each grow to the function's memory cap.
const poolMemBudget = 256 << 20

// nodePooledMem is the idle pooled memory of every function on this node;
// nodePoolBudget caps it so RSS does not climb with the function count.
var (
	nodePooledMem  atomic.Int64
	nodePoolBudget = nodePoolBudgetFor(totalMemory())
)

func totalMemory() uint64 {
	if vmStat, err := mem.VirtualMemory(); err == nil {
		return vmStat.Total
	}
	return 0
}

// nodePoolBudgetFor is a quarter of the node's memory (512 MiB if unknown), at least 128 MiB.
func nodePoolBudgetFor(total uint64) int64 {
	if total == 0 {
		return 512 << 20
	}
	return max(int64(total/4), 128<<20)
}

func shouldRetire(useMem uint32, capBytes uint64) bool {
	return uint64(useMem) > capBytes*2/3
}

func (i *instance) Free() error {
	if i.failed {
		i.Close()
		return nil
	}

	i.parent.recordModules(i.runtime)

	useMem, err := i.usedMemory()
	if err != nil {
		i.Close()
		return err
	}

	capBytes := uint64(i.parent.vmConfig.MemoryLimitPages) * uint64(vm.MemoryPageSize)
	if shouldRetire(useMem, capBytes) {
		// an instance retired before ever being pooled means the function's
		// memory config leaves no headroom: every call will cold-start.
		if !i.pooled && i.parent.noPoolWarned.CompareAndSwap(false, true) {
			logger.Warnf(
				"function `%s` (%s) uses %d bytes of its %d-byte memory cap; instances never pool and every call cold-starts — increase the function's memory",
				i.parent.config.Name, i.parent.serviceable.Id(), useMem, capBytes,
			)
		}
		i.Close()
		return nil
	}

	if i.parent.pooledMem.Load()+int64(useMem) > poolMemBudget ||
		nodePooledMem.Load()+int64(useMem) > nodePoolBudget {
		i.Close()
		return nil
	}

	i.pooled = true
	i.unhold()
	i.parent.repool(i, useMem)
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
	i.unhold()
	return i.runtime.Close()
}

// unpool takes a pooled instance's memory off its function's pool budget.
func unpool(inst Instance) uint32 {
	i, ok := inst.(*instance)
	if !ok {
		return 0
	}
	mem := i.poolMem
	i.poolMem = 0
	i.parent.pooledMem.Add(-int64(mem))
	nodePooledMem.Add(-int64(mem))
	return mem
}

// hold takes the project slot of a pooled instance checked out for a request.
func hold(inst Instance) error {
	i, ok := inst.(*instance)
	if !ok {
		return nil
	}
	if err := i.parent.acquire(i.parent.serviceable.Project(), i.parent.config.Memory); err != nil {
		return err
	}
	i.held.Store(true)
	return nil
}

// unhold gives the slot back once, when the instance is pooled or closed.
func (i *instance) unhold() {
	if i.held.CompareAndSwap(true, false) {
		release(i.parent.serviceable.Project(), i.parent.config.Memory)
	}
}

// repool puts back a pooled instance that could not be checked out.
// mem is the instance's memory, counted against poolMemBudget while pooled.
// It never blocks a caller (it may hold a request lock) on a full pool.
func (f *Function) repool(inst Instance, mem uint32) {
	f.shutdownMu.RLock()
	defer f.shutdownMu.RUnlock()
	if f.drained {
		inst.Close()
		return
	}

	if i, ok := inst.(*instance); ok {
		i.poolMem = mem
		f.pooledMem.Add(int64(mem))
		nodePooledMem.Add(int64(mem))
	}
	select {
	case f.availableInstances <- inst:
	default:
		unpool(inst)
		inst.Close()
	}
}

// checkout takes a pooled instance or creates one, holding a project slot for it.
func (f *Function) checkout() (Instance, error) {
	select {
	case pooled := <-f.availableInstances:
		mem := unpool(pooled)
		if err := hold(pooled); err != nil {
			f.repool(pooled, mem)
			return nil, err
		}
		return pooled, nil
	default:
	}

	project, mem := f.serviceable.Project(), f.config.Memory
	if err := f.acquire(project, mem); err != nil {
		return nil, err
	}
	runtime, sdk, err := f.instantiate()
	if err != nil {
		release(project, mem)
		return nil, err
	}
	inst := &instance{runtime: runtime, sdk: sdk, parent: f}
	inst.held.Store(true)
	return inst, nil
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
		return nil, platformerr.New(platformerr.NodeBusy, errors.New("instance request channel is full"))
	}

	select {
	case <-f.ctx.Done():
		instCh.leave()
		return nil, fmt.Errorf("function context done with: %w", f.ctx.Err())
	case <-ctx.Done():
		instCh.leave()
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
	inst, err := f.checkout()
	if err == nil {
		reqCh.give(inst)
		return
	}

	// A refusal is normal back-pressure, and can come once per request.
	logf := logger.Errorf
	if c := platformerr.CodeOf(err); c == platformerr.NodeBusy || c == platformerr.OverQuota {
		logf = logger.Debugf
	}
	logf("creating new instance failed with: %s", err.Error())
	// Refuse unless an instance is free right now: the caller's context lasts
	// as long as the function, and waiting would stall its whole request queue.
	select {
	case <-reqCh.ctx.Done():
		reqCh.err = fmt.Errorf("instance request context done with: %w", reqCh.ctx.Err())
		reqCh.give(nil)
	case pooled := <-f.availableInstances:
		mem := unpool(pooled)
		if err := hold(pooled); err != nil {
			f.repool(pooled, mem)
			reqCh.err = err
			reqCh.give(nil)
			return
		}
		reqCh.give(pooled)
	case <-f.ctx.Done():
		return
	default:
		reqCh.err = err
		reqCh.give(nil)
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
			return nil, nil, platformerr.New(platformerr.NodeBusy, fmt.Errorf("insufficient system memory available: %d bytes", availableMemory))
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

	rt, err = instance.Runtime()
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

// acquire reserves the project's share for one more instance.
func (f *Function) acquire(project string, mem uint64) error {
	return acquire(project, mem, f.serviceable.Service().Caps(project))
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
