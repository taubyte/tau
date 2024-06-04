package taubyte

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/dns"
	"github.com/taubyte/tau/pkg/vm-low-orbit/ethereum"
	"github.com/taubyte/tau/pkg/vm-low-orbit/globals"
	"github.com/taubyte/tau/pkg/vm-low-orbit/pubsub"
	"github.com/taubyte/tau/pkg/vm-low-orbit/self"
	"github.com/taubyte/tau/pkg/vm-low-orbit/storage"

	"github.com/taubyte/tau/pkg/vm-low-orbit/crypto/rand"
	kvdb "github.com/taubyte/tau/pkg/vm-low-orbit/database/client"
	"github.com/taubyte/tau/pkg/vm-low-orbit/event"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
	"github.com/taubyte/tau/pkg/vm-low-orbit/http/client"
	"github.com/taubyte/tau/pkg/vm-low-orbit/i2mv/fifo"
	"github.com/taubyte/tau/pkg/vm-low-orbit/i2mv/memoryView"
	ipfsClient "github.com/taubyte/tau/pkg/vm-low-orbit/ipfs/client"
	p2pClient "github.com/taubyte/tau/pkg/vm-low-orbit/p2p"
)

type pluginInstance struct {
	eventApi
	instance  vm.Instance
	factories []vm.Factory
}

// create an instance of the plugin that  can be Loaded by a wasm instance
func (p *plugin) New(instance vm.Instance) (vm.PluginInstance, error) {
	if Plugin() == nil {
		return nil, errors.New("initialize plugin in first")
	}

	helperMethods := helpers.New(instance.Context().Context())
	eventApi := event.New(instance, helperMethods)
	return &pluginInstance{
		instance: instance,
		eventApi: eventApi,
		factories: []vm.Factory{
			eventApi,
			ethereum.New(instance, p.pubsubNode, helperMethods),
			client.New(instance, helperMethods),
			ipfsClient.New(instance, p.ipfsNode, helperMethods),
			pubsub.New(instance, p.pubsubNode, helperMethods),
			storage.New(instance, p.storageNode, helperMethods),
			kvdb.New(instance, p.databaseNode, helperMethods),
			p2pClient.New(instance, p.p2pNode, helperMethods),
			dns.New(instance, helperMethods),
			self.New(instance, helperMethods),
			globals.New(instance, p.databaseNode, helperMethods),
			rand.New(instance, helperMethods),
			memoryView.New(instance, helperMethods),
			fifo.New(instance, helperMethods),
		},
	}, nil
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
