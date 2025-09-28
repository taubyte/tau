package smartOps

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/node"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/resource"
)

type pluginInstance struct {
	resourceApi
	instance  vm.Instance
	factories []vm.Factory
}

// create an instance of the plugin that  can be Loaded by a wasm instance
func (p *plugin) New(instance vm.Instance) (vm.PluginInstance, error) {
	if _plugin == nil {
		return nil, errors.New("initialize Plugin in first")
	}

	helperMethods := helpers.New(instance.Context().Context())
	resourceApi := resource.New(instance, helperMethods)

	return &pluginInstance{
		resourceApi: resourceApi,
		instance:    instance,
		factories: []vm.Factory{
			resourceApi,
			node.New(instance, p.smartOpNode, helperMethods),
		},
	}, nil
}

func (i *pluginInstance) LoadFactory(factory vm.Factory, hm vm.HostModule) error {
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
func (i *pluginInstance) Load(hm vm.HostModule) (modInstance vm.ModuleInstance, err error) {
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
