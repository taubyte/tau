//go:build web3
// +build web3

package taubyte

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/core/services/substrate/components/ipfs"
	"github.com/taubyte/tau/core/services/substrate/components/p2p"
	"github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/core/services/substrate/components/storage"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/crypto/rand"
	kvdb "github.com/taubyte/tau/pkg/vm-low-orbit/database/client"
	"github.com/taubyte/tau/pkg/vm-low-orbit/dns"
	"github.com/taubyte/tau/pkg/vm-low-orbit/ethereum"
	"github.com/taubyte/tau/pkg/vm-low-orbit/event"
	"github.com/taubyte/tau/pkg/vm-low-orbit/globals"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
	"github.com/taubyte/tau/pkg/vm-low-orbit/http/client"
	"github.com/taubyte/tau/pkg/vm-low-orbit/i2mv/fifo"
	"github.com/taubyte/tau/pkg/vm-low-orbit/i2mv/memoryView"
	ipfsClient "github.com/taubyte/tau/pkg/vm-low-orbit/ipfs/client"
	p2pClient "github.com/taubyte/tau/pkg/vm-low-orbit/p2p"
	"github.com/taubyte/tau/pkg/vm-low-orbit/self"
)

type plugin struct {
	ctx          context.Context
	ctxC         context.CancelFunc
	ipfsNode     ipfs.Service
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
	case ipfs.Service:
		p.ipfsNode = service
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

func IpfsNode(node ipfs.Service) Option {
	return func() (err error) {
		if _plugin == nil {
			return errNilPlugin
		}

		if err = _plugin.setNode(node); err != nil {
			return fmt.Errorf("setting ipfs node failed with: %w", err)
		}

		return
	}
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
