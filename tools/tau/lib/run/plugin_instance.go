package run

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/event"
)

type minimalPluginInstance struct {
	eventFactory *event.Factory
	factories    []vm.Factory
}

func (pi *minimalPluginInstance) LoadFactory(factory vm.Factory, hm vm.HostModule) error {
	defs := make([]*vm.HostModuleFunctionDefinition, 0)
	m := reflect.ValueOf(factory)
	mT := reflect.TypeOf(factory)
	for i := 0; i < m.NumMethod(); i++ {
		mt := m.Method(i)
		mtT := mT.Method(i)
		if strings.HasPrefix(mtT.Name, "W_") {
			defs = append(defs, &vm.HostModuleFunctionDefinition{
				Name:    mtT.Name[2:],
				Handler: mt.Interface(),
			})
		}
	}
	return hm.Functions(defs...)
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
