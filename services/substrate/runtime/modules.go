package runtime

import (
	"fmt"

	"github.com/taubyte/tau/core/vm"
	resolver "github.com/taubyte/tau/pkg/vm/resolvers/taubyte"
)

// recordModules remembers where each loaded wasm module resolved to.
// shortcut: resolved after loading, so a push landing mid-call is missed until the next push; record inside the vm loader if that matters.
func (f *Function) recordModules(rt vm.Runtime) {
	f.modulesLock.Lock()
	defer f.modulesLock.Unlock()
	if f.modules != nil {
		return
	}

	lookup := resolver.New(f.serviceable.Service().Tns())
	modules := make(map[string]string)
	for _, name := range rt.Modules() {
		if addr, err := lookup.Lookup(f.vmContext, name); err == nil {
			modules[name] = addr.String()
		}
	}
	f.modules = modules
}

// ModulesChanged returns an error when a module this function loaded now
// resolves elsewhere, e.g. after a library push that left the config commit alone.
func (f *Function) ModulesChanged() error {
	if f == nil {
		return nil
	}

	f.modulesLock.Lock()
	modules := f.modules
	f.modulesLock.Unlock()
	if len(modules) == 0 {
		return nil
	}

	lookup := resolver.New(f.serviceable.Service().Tns())
	for name, was := range modules {
		addr, err := lookup.Lookup(f.vmContext, name)
		if err != nil {
			return fmt.Errorf("resolving module `%s` failed with: %w", name, err)
		}
		if addr.String() != was {
			return fmt.Errorf("module `%s` moved from `%s` to `%s`", name, was, addr.String())
		}
	}

	return nil
}
