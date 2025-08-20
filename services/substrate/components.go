//go:build !web3
// +build !web3

package substrate

import (
	iface "github.com/taubyte/tau/core/services/substrate"
	databaseIface "github.com/taubyte/tau/core/services/substrate/components/database"
	p2pIface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	pubSubIface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
	tbPlugins "github.com/taubyte/tau/pkg/vm-low-orbit"
	httpIface "github.com/taubyte/tau/services/substrate/components/http"
)

// TODO: All of these components interfaces can be removed
type components struct {
	http     *httpIface.Service
	pubsub   pubSubIface.Service
	database databaseIface.Service
	storage  storageIface.Service
	p2p      p2pIface.Service
	counters iface.CounterService
	smartops iface.SmartOpsService
}

func (c *components) config() []tbPlugins.Option {
	return []tbPlugins.Option{
		tbPlugins.PubsubNode(c.pubsub),
		tbPlugins.DatabaseNode(c.database),
		tbPlugins.StorageNode(c.storage),
		tbPlugins.P2PNode(c.p2p),
	}
}

func (c *components) close() {
	c.http.Close()
	c.pubsub.Close()
	c.database.Close()
	c.storage.Close()
	c.p2p.Close()
	c.counters.Close()
	c.smartops.Close()
}
