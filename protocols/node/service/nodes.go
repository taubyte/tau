package service

import (
	"fmt"

	counters "bitbucket.org/taubyte/go-node-counters"
	database "bitbucket.org/taubyte/go-node-database"
	http "bitbucket.org/taubyte/go-node-http"
	ipfs "bitbucket.org/taubyte/go-node-ipfs"
	p2p "bitbucket.org/taubyte/go-node-p2p"
	pubSub "bitbucket.org/taubyte/go-node-pubsub"
	smartOps "bitbucket.org/taubyte/go-node-smartops"
	storage "bitbucket.org/taubyte/go-node-storage"
	moodyCommon "github.com/taubyte/go-interfaces/moody"

	commonIface "github.com/taubyte/go-interfaces/services/common"
)

func attachNodesError(name string, err error) error {
	err = fmt.Errorf("creating node %s failed with %s", name, err.Error())
	logger.Error(moodyCommon.Object{"message": err.Error()})

	return err
}

func (srv *Service) attachNodes(config *commonIface.GenericConfig) (err error) {
	// Needs to happen first, as others depend on it
	if err = srv.attachNodeCounters(config); err != nil {
		return attachNodesError("counters", err)
	}

	// Needs to happen second, as others depend on it
	if err = srv.attachNodeSmartOps(config); err != nil {
		return attachNodesError("smartops", err)
	}

	if err = srv.attachNodePubSub(config); err != nil {
		return attachNodesError("pubsub", err)
	}

	if err = srv.attachNodeIpfs(config); err != nil {
		return attachNodesError("ipfs", err)
	}

	if err = srv.attachNodeDatabase(config); err != nil {
		return attachNodesError("database", err)
	}

	if err = srv.attachNodeStorage(config); err != nil {
		return attachNodesError("storage", err)
	}

	if err = srv.attachNodeP2P(config); err != nil {
		return attachNodesError("p2p", err)
	}

	if err = srv.attachNodeHttp(config); err != nil {
		return attachNodesError("http", err)
	}

	return nil
}

func (srv *Service) attachNodeHttp(config *commonIface.GenericConfig) (err error) {
	ops := []http.Option{}

	if config.DevMode {
		ops = append(ops, http.Dev())
	}

	if config.Verbose {
		ops = append(ops, http.Verbose())
	}

	if commonIface.Deployment == commonIface.Odo {
		ops = append(ops, http.DvKey(config.DVPublicKey))
	}

	srv.nodeHttp, err = http.New(srv, ops...)
	return
}

func (srv *Service) attachNodePubSub(config *commonIface.GenericConfig) (err error) {
	ops := []pubSub.Option{}

	if config.DevMode {
		ops = append(ops, pubSub.Dev())
	}

	if config.Verbose {
		ops = append(ops, pubSub.Verbose())
	}

	srv.nodePubSub, err = pubSub.New(srv, ops...)
	return
}

func (srv *Service) attachNodeIpfs(config *commonIface.GenericConfig) (err error) {
	ipfsPort, ok := config.Ports["ipfs"]
	if !ok {
		err = fmt.Errorf("did not find ipfs port in config")
		return

	}

	srv.nodeIpfs, err = ipfs.New(srv.node.Context(), ipfs.Public(), ipfs.Listen([]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", ipfsPort)}))
	return
}

func (srv *Service) attachNodeDatabase(config *commonIface.GenericConfig) (err error) {
	ops := []database.Option{}

	if config.DevMode {
		ops = append(ops, database.Dev())
	}

	srv.nodeDatabase, err = database.New(srv, ops...)
	return
}

func (srv *Service) attachNodeStorage(config *commonIface.GenericConfig) (err error) {
	ops := []storage.Option{}

	if config.DevMode {
		ops = append(ops, storage.Dev())
	}

	srv.nodeStorage, err = storage.New(srv, ops...)
	return
}

func (srv *Service) attachNodeP2P(config *commonIface.GenericConfig) (err error) {
	ops := []p2p.Option{}

	if config.DevMode {
		ops = append(ops, p2p.Dev())
	}

	if config.Verbose {
		ops = append(ops, p2p.Verbose())
	}

	srv.nodeP2P, err = p2p.New(srv, ops...)
	return
}

func (srv *Service) attachNodeCounters(config *commonIface.GenericConfig) (err error) {
	srv.nodeCounters, err = counters.New(srv)
	return
}

func (srv *Service) attachNodeSmartOps(config *commonIface.GenericConfig) (err error) {
	ops := []smartOps.Option{}

	if config.DevMode {
		ops = append(ops, smartOps.Dev())
	}

	if config.Verbose {
		ops = append(ops, smartOps.Verbose())
	}

	srv.nodeSmartOps, err = smartOps.New(srv, ops...)
	return
}
