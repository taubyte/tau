package service

import (
	"context"
	"fmt"

	moody "bitbucket.org/taubyte/go-moody-blues"
	kv "bitbucket.org/taubyte/kvdb/database"
	streams "bitbucket.org/taubyte/p2p/streams/service"
	commonIface "github.com/taubyte/go-interfaces/services/common"
	"github.com/taubyte/go-interfaces/services/seer"
	seerClient "github.com/taubyte/odo/clients/p2p/seer"

	dreamlandCommon "bitbucket.org/taubyte/dreamland/common"
	commonSpec "github.com/taubyte/go-specs/common"
	protocolsCommon "github.com/taubyte/odo/protocols/common"
	"github.com/taubyte/odo/protocols/tns/engine"

	configutils "bitbucket.org/taubyte/p2p/config"
)

var (
	logger, _ = moody.New("tns.service")
)

func New(ctx context.Context, config *commonIface.GenericConfig) (*Service, error) {
	srv := &Service{}

	if config == nil {
		_cnf := &commonIface.GenericConfig{}
		_cnf.Bootstrap = true

		config = _cnf
	}

	err := config.Build(commonIface.ConfigBuilder{
		DefaultP2PListenPort: protocolsCommon.TnsDefaultP2PListenPort,
		DevP2PListenFormat:   dreamlandCommon.DefaultP2PListenFormat,
	})
	if err != nil {
		return nil, err
	}

	if config.Node == nil {
		srv.node, err = configutils.NewNode(ctx, config, protocolsCommon.Tns)
		if err != nil {
			return nil, err
		}
	} else {
		srv.node = config.Node
	}

	srv.db, err = kv.New(logger.Std(), srv.node, protocolsCommon.Tns, 5)
	if err != nil {
		return nil, err
	}

	srv.engine, err = engine.New(srv.db, engine.Prefix...)
	if err != nil {
		return nil, err
	}

	// should end if any of the two contexts ends

	// P2P
	srv.stream, err = streams.New(srv.node, protocolsCommon.Tns, commonSpec.TnsProtocol)
	if err != nil {
		return nil, err
	}

	srv.setupStreamRoutes()

	// For Odo
	clientNode := srv.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
	}
	sc, err := seerClient.New(ctx, clientNode)
	if err != nil {
		return nil, fmt.Errorf("failed creating seer client error: %v", err)
	}

	err = config.StartSeerBeacon(sc, seer.ServiceTypeTns)
	if err != nil {
		return nil, err
	}

	return srv, nil
}

func (srv *Service) Close() error {
	// TODO use debug logger
	fmt.Println("Closing", protocolsCommon.Tns)
	defer fmt.Println(protocolsCommon.Tns, "closed")

	// node.ctx
	srv.stream.Stop()

	// Maybe not??
	// ctx, needs to close after node as node will try to close it's store
	srv.db.Close()

	// ctx
	srv.node.Close()
	return nil
}
