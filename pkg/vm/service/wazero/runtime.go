package service

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/core/vm"
	"github.com/tetratelabs/wazero"
	api "github.com/tetratelabs/wazero/api"

	crand "crypto/rand"
)

func (r *runtime) Close() error {
	if err := r.runtime.Close(r.instance.ctx.Context()); err != nil {
		return err
	}

	close(r.wasiStartDone)

	return nil
}

func (r *runtime) Expose(name string) (vm.HostModule, error) {
	return &hostModule{
		ctx:       r.instance.ctx,
		name:      name,
		runtime:   r,
		functions: make(map[string]functionDef),
		memories:  make(map[string]memoryPages),
		globals:   make(map[string]interface{}),
	}, nil
}

func (r *runtime) Stdout() io.Reader {
	return r.instance.Stdout()
}

func (r *runtime) Stderr() io.Reader {
	return r.instance.Stderr()
}

func (r *runtime) Attach(plugin vm.Plugin) (vm.PluginInstance, vm.ModuleInstance, error) {
	if plugin == nil {
		return nil, nil, fmt.Errorf("plugin cannot be nil")
	}

	pi, err := plugin.New(r.instance)
	if err != nil {
		return nil, nil, fmt.Errorf("creating new plugin instance failed with: %s", err)
	}

	hm := &hostModule{
		ctx:       r.instance.ctx,
		name:      plugin.Name(),
		runtime:   r,
		functions: make(map[string]functionDef),
		memories:  make(map[string]memoryPages),
		globals:   make(map[string]interface{}),
	}

	minst, err := pi.Load(hm)
	if err != nil {
		return nil, nil, fmt.Errorf("loading plugin instance failed with: %s", err)
	}

	return pi, minst, nil
}

func (r *runtime) Module(name string) (vm.ModuleInstance, error) {
	return r.module(name)
}

func (r *runtime) module(name string) (vm.ModuleInstance, error) {
	modInst := r.runtime.Module(name)
	if modInst == nil {
		module, err := r.instance.service.Source().Module(r.instance.ctx, name)
		if err != nil {
			return nil, fmt.Errorf("loading module `%s` failed with: %s", name, err)
		}

		compiled, err := r.runtime.CompileModule(r.instance.ctx.Context(), module)
		if err != nil {
			return nil, fmt.Errorf("getting compiled module failed with: %s", err)
		}

		deps := make(map[string]struct{})
		hasReady := false
		for _, def := range compiled.ImportedFunctions() {
			dep, fx, _ := def.Import()
			if dep == "env" {
				if fx == "_ready" {
					hasReady = true
				}
				continue
			}

			deps[dep] = struct{}{}

		}

		for dep := range deps {
			_, err := r.module(dep)
			if err != nil {
				return nil, fmt.Errorf("loading module `%s` dependency `%s` failed with: %s", name, dep, err)
			}
		}

		modInst, err = r.instantiate(name, compiled, hasReady)
		if err != nil {
			return nil, fmt.Errorf("creating an instance of module `%s` failed with: %s", name, err)
		}
	}

	return &moduleInstance{
		parent: r,
		module: modInst,
		ctx:    r.instance.ctx.Context(),
	}, nil
}

func (r *runtime) instantiate(name string, compiled wazero.CompiledModule, hasReady bool) (api.Module, error) {

	config := wazero.
		NewModuleConfig().
		WithName(name).
		WithStartFunctions(). // don't run _start: we need to start it in a go routine
		WithFS(afero.NewIOFS(r.instance.fs)).
		WithStdout(r.instance.output).
		WithStderr(r.instance.outputErr).
		WithArgs(name).
		WithSysWalltime().
		WithSysNanotime().
		WithSysNanosleep().
		WithRandSource(crand.Reader)

	ctx := r.instance.ctx.Context()
	m, err := r.runtime.InstantiateModule(ctx, compiled, config)
	if err != nil {
		return nil, fmt.Errorf("instantiating compiled module `%s` failed with: %s", name, err)
	}

	if _start := m.ExportedFunction("_start"); _start != nil {
		if hasReady {
			go func() {
				_start.Call(ctx)
			}()

			select {
			case <-ctx.Done():
			case <-r.wasiStartDone:
			}
		} else {
			_start.Call(ctx)
		}
	}

	return m, nil
}

func (r *runtime) defaultModuleFunctions() []*vm.HostModuleFunctionDefinition {
	return []*vm.HostModuleFunctionDefinition{
		{
			Name: "_ready",
			Handler: func(ctx context.Context, module vm.Module) {
				r.wasiStartDone <- true
			},
		},
		{
			Name: "_sleep",
			Handler: func(ctx context.Context, dur int64) {
				select {
				case <-ctx.Done():
				case <-time.After(time.Duration(dur)):
				}
			},
		},
		{
			Name: "_log",
			Handler: func(ctx context.Context, module vm.Module, data uint32, dataLen uint32) {
				msgBuf, _ := module.Memory().Read(data, dataLen)
				fmt.Println(string(msgBuf))
			},
		},
	}
}
