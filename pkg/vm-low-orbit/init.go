//go:build !web3
// +build !web3

package taubyte

import (
	"context"
	"errors"

	"github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/core/services/substrate/components/p2p"
	"github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/core/services/substrate/components/storage"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/crypto/rand"
	"github.com/taubyte/tau/pkg/vm-low-orbit/dns"
	"github.com/taubyte/tau/pkg/vm-low-orbit/event"
	"github.com/taubyte/tau/pkg/vm-low-orbit/globals"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
	"github.com/taubyte/tau/pkg/vm-low-orbit/http/client"
	"github.com/taubyte/tau/pkg/vm-low-orbit/i2mv/fifo"
	"github.com/taubyte/tau/pkg/vm-low-orbit/i2mv/memoryView"
	p2pClient "github.com/taubyte/tau/pkg/vm-low-orbit/p2p"
	"github.com/taubyte/tau/pkg/vm-low-orbit/self"

	vmpubsub "github.com/taubyte/tau/pkg/vm-low-orbit/pubsub"
	vmstorage "github.com/taubyte/tau/pkg/vm-low-orbit/storage"

	kvdb "github.com/taubyte/tau/pkg/vm-low-orbit/database/client"
)

type plugin struct {
	ctx          context.Context
	ctxC         context.CancelFunc
	pubsubNode   pubsub.Service
	databaseNode database.Service
	storageNode  storage.Service
	p2pNode      p2p.Service
}

func (p *plugin) setNode(nodeService interface{}) error {
	if nodeService == nil {
		return errors.New("node service is nil")
	}

	switch service := nodeService.(type) {
	case pubsub.Service:
		p.pubsubNode = service
	case database.Service:
		p.databaseNode = service
	case storage.Service:
		p.storageNode = service
	case p2p.Service:
		p.p2pNode = service
	default:
		return errors.New("not a valid node service")
	}
	return nil
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
			client.New(instance, helperMethods),
			vmpubsub.New(instance, p.pubsubNode, helperMethods),
			vmstorage.New(instance, p.storageNode, helperMethods),
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
