package taubyte

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/taubyte/tau/core/vm"
)

type pluginInstance struct {
	eventApi
	instance  vm.Instance
	factories []vm.Factory
}

// factoryWMethods caches, per factory type, the W_-prefixed method indexes/names
// discovered via reflection. Discovery is the same for every instance of a given
// factory type, and reflection is expensive enough to matter on wasm cold start.
var factoryWMethods sync.Map // reflect.Type -> []struct{ index int; name string }

func (i *pluginInstance) LoadFactory(factory vm.Factory, hm vm.HostModule) error {
	mT := reflect.TypeOf(factory)

	var entries []struct {
		index int
		name  string
	}
	if cached, ok := factoryWMethods.Load(mT); ok {
		entries = cached.([]struct {
			index int
			name  string
		})
	} else {
		for i := 0; i < mT.NumMethod(); i++ {
			mtT := mT.Method(i)
			if strings.HasPrefix(mtT.Name, "W_") {
				entries = append(entries, struct {
					index int
					name  string
				}{index: i, name: mtT.Name[2:]})
			}
		}
		// two goroutines racing the same type both compute the same value; last store wins
		factoryWMethods.Store(mT, entries)
	}

	m := reflect.ValueOf(factory)
	defs := make([]*vm.HostModuleFunctionDefinition, 0, len(entries))
	for _, entry := range entries {
		defs = append(defs, &vm.HostModuleFunctionDefinition{
			Name:    entry.name,
			Handler: m.Method(entry.index).Interface(),
		})
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
