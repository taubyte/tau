package taubyte

import (
	"errors"
	"fmt"

	"github.com/taubyte/tau/core/vm"
)

type pluginInstance struct {
	eventApi
	instance  vm.Instance
	factories []vm.Factory
}

// LoadFactory registers a factory's host functions from its generated,
// reflection-free HostFunctions() (see hostfn-gen). Every factory in the plugin
// implements vm.HostFunctionProvider.
func (i *pluginInstance) LoadFactory(factory vm.Factory, hm vm.HostModule) error {
	provider, ok := factory.(vm.HostFunctionProvider)
	if !ok {
		return fmt.Errorf("factory %q (%T) does not provide host functions", factory.Name(), factory)
	}
	return hm.Functions(provider.HostFunctions()...)
}
func (i *pluginInstance) Load(hm vm.HostModule) (moduleInstance vm.ModuleInstance, err error) {
	for _, factory := range i.factories {
		if loadErr := i.LoadFactory(factory, hm); loadErr != nil {
			if err == nil {
				err = errors.New("load failed with: ")
			}

			err = fmt.Errorf("%s\n%w", err, loadErr)
		}
	}
	if err != nil {
		return nil, err
	}

	return hm.Compile()
}

func (i *pluginInstance) Close() (err error) {
	for _, factory := range i.factories {
		if closeErr := factory.Close(); closeErr != nil {
			if err == nil {
				err = errors.New("close failed with: ")
			}

			err = fmt.Errorf("%s\n%w", err, closeErr)
		}
	}

	return
}
