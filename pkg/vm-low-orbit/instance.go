package taubyte

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/taubyte/tau/core/vm"
)

type pluginInstance struct {
	eventApi
	instance  vm.Instance
	factories []vm.Factory
}

func (i *pluginInstance) LoadFactory(factory vm.Factory, hm vm.HostModule) error {
	if err := factory.Load(hm); err != nil {
		return err
	}

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
