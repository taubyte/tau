package substrate

import (
	"fmt"

	"github.com/taubyte/tau/config"
	counters "github.com/taubyte/tau/protocols/substrate/components/counters"
	database "github.com/taubyte/tau/protocols/substrate/components/database"
	http "github.com/taubyte/tau/protocols/substrate/components/http"
	ipfs "github.com/taubyte/tau/protocols/substrate/components/ipfs"
	p2p "github.com/taubyte/tau/protocols/substrate/components/p2p"
	pubSub "github.com/taubyte/tau/protocols/substrate/components/pubsub"
	smartOps "github.com/taubyte/tau/protocols/substrate/components/smartops"
	storage "github.com/taubyte/tau/protocols/substrate/components/storage"
)

func attachNodesError(name string, err error) error {
	err = fmt.Errorf("creating node %s failed with %s", name, err.Error())
	logger.Error(err)

	return err
}

func (srv *Service) Verbose() bool {
	return srv.verbose
}

func (srv *Service) attachNodes(config *config.Node) (err error) {
	// Needs to happen first, as others depend on it
	if err = srv.attachNodeCounters(); err != nil {
		return attachNodesError("counters", err)
	}

	// Needs to happen second, as others depend on it
	if err = srv.attachNodeSmartOps(); err != nil {
		return attachNodesError("smartops", err)
	}

	if err = srv.attachNodePubSub(); err != nil {
		return attachNodesError("pubsub", err)
	}

	if err = srv.attachNodeIpfs(config); err != nil {
		return attachNodesError("ipfs", err)
	}

	if err = srv.attachNodeDatabase(); err != nil {
		return attachNodesError("database", err)
	}

	if err = srv.attachNodeStorage(); err != nil {
		return attachNodesError("storage", err)
	}

	if err = srv.attachNodeP2P(); err != nil {
		return attachNodesError("p2p", err)
	}

	if err = srv.attachNodeHttp(config); err != nil {
		return attachNodesError("http", err)
	}

	return nil
}

func (srv *Service) attachNodeHttp(config *config.Node) (err error) {
	srv.components.http, err = http.New(srv, http.DvKey(config.DomainValidation.PublicKey))
	return
}

func (srv *Service) attachNodePubSub() (err error) {
	srv.components.pubsub, err = pubSub.New(srv)
	return
}

func (srv *Service) attachNodeIpfs(config *config.Node) (err error) {
	ipfsPort, ok := config.Ports["ipfs"]
	if !ok {
		err = fmt.Errorf("did not find ipfs port in config")
		return

	}

	srv.components.ipfs, err = ipfs.New(srv.node.Context(), ipfs.Public(), ipfs.Listen([]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", ipfsPort)}))
	return
}

func (srv *Service) attachNodeDatabase() (err error) {
	srv.components.database, err = database.New(srv, srv.databases)
	return
}

func (srv *Service) attachNodeStorage() (err error) {
	srv.components.storage, err = storage.New(srv, srv.databases)
	return
}

func (srv *Service) attachNodeP2P() (err error) {
	srv.components.p2p, err = p2p.New(srv)
	return
}

func (srv *Service) attachNodeCounters() (err error) {
	srv.components.counters, err = counters.New(srv)
	return
}

func (srv *Service) attachNodeSmartOps() (err error) {
	srv.components.smartops, err = smartOps.New(srv)
	return
}
