package vm

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/core/vm"
)

func (p *pluginInstance) cleanup() error {
	return p.satellite.Close()
}

func (p *pluginInstance) reload() (err error) {
	p.satellite, err = p.plugin.getLink()
	return
}

func (p *pluginInstance) Load(hm vm.HostModule) (vm.ModuleInstance, error) {
	p.plugin.lock.RLock()
	defer p.plugin.lock.RUnlock()

	defs, err := p.satellite.Symbols(p.instance.Context().Context())
	if err != nil {
		return nil, fmt.Errorf("getting (satellite) symbols failed with: %w", err)
	}

	funcDefs := make([]*vm.HostModuleFunctionDefinition, len(defs))
	for idx, def := range defs {
		funcDefs[idx] = &vm.HostModuleFunctionDefinition{
			Name:        def.Name(),
			ParamTypes:  def.ParamTypes(),
			ResultTypes: def.ResultTypes(),
			Stack:       p.stackHandler(def.Name(), len(def.ParamTypes())),
		}
	}

	hm.Functions(funcDefs...)
	return hm.Compile()
}

// stackHandler bridges a wasm host call straight to the RPC satellite: wasm
// params and results are already raw uint64 slots, and satellite.Call speaks
// []uint64, so this is a direct passthrough with no reflection or per-value
// conversion (the engine reuses stack for both params and results).
func (p *pluginInstance) stackHandler(name string, nParams int) vm.StackHostFunction {
	return func(ctx context.Context, module vm.Module, stack []uint64) {
		in := make([]uint64, nParams)
		copy(in, stack[:nParams])

		p.plugin.lock.RLock()
		defer p.plugin.lock.RUnlock()

		out, err := p.satellite.Call(ctx, module, name, in)
		if err != nil {
			panic(fmt.Sprintf("[rpc] calling `%s/%s` failed with: %s (ctx.err=%s)", module.Name(), name, err, ctx.Err()))
		}
		copy(stack, out)
	}
}

func (p *pluginInstance) Close() error {
	p.plugin.lock.Lock()
	defer p.plugin.lock.Unlock()
	p.close()
	return nil
}

func (p *pluginInstance) close() error {
	delete(p.plugin.instances, p)
	p.cleanup()
	return nil
}
