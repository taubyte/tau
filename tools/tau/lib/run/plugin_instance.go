package run

import (
	"errors"
	"fmt"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/event"
)

type minimalPluginInstance struct {
	eventFactory *event.Factory
	factories    []vm.Factory
}

func (pi *minimalPluginInstance) LoadFactory(factory vm.Factory, hm vm.HostModule) error {
	provider, ok := factory.(vm.HostFunctionProvider)
	if !ok {
		return fmt.Errorf("factory %q (%T) does not provide host functions", factory.Name(), factory)
	}
	provider.RegisterHostFunctions(hm.Builder())
	return nil
}

func (pi *minimalPluginInstance) Load(hm vm.HostModule) (vm.ModuleInstance, error) {
	var err error
	for _, factory := range pi.factories {
		if loadErr := pi.LoadFactory(factory, hm); loadErr != nil {
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

func (pi *minimalPluginInstance) Close() error {
	var err error
	for _, factory := range pi.factories {
		if closeErr := factory.Close(); closeErr != nil {
			if err == nil {
				err = errors.New("close failed with: ")
			}
			err = fmt.Errorf("%s\n%w", err, closeErr)
		}
	}
	return err
}
