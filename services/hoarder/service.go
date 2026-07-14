package hoarder

import (
	"context"
	"fmt"
	"path"

	hoarderClient "github.com/taubyte/tau/clients/p2p/hoarder"
	tnsApi "github.com/taubyte/tau/clients/p2p/tns"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	streamClient "github.com/taubyte/tau/p2p/streams/client"
	streams "github.com/taubyte/tau/p2p/streams/service"
	tauConfig "github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/pkg/kvdb"
	protocolCommon "github.com/taubyte/tau/services/common"
)

func New(ctx context.Context, cfg tauConfig.Config) (service hoarderIface.Service, err error) {
	s := &Service{
		ldr:     newLoader(),
		members: make(map[string]*member),
	}

	defer func() {
		if err != nil {
			logger.Errorf("starting hoarder service failed with: %s", err.Error())
			s.Close()
		}
	}()

	if s.node = cfg.Node(); s.node == nil {
		s.node, err = tauConfig.NewNode(ctx, cfg, path.Join(cfg.Root(), protocolCommon.Hoarder))
		if err != nil {
			return nil, fmt.Errorf("new peer node failed with: %w", err)
		}
	}

	clientNode := s.node
	if cfg.ClientNode() != nil {
		clientNode = cfg.ClientNode()
	}

	s.zone = cfg.Cluster()

	if s.stream, err = streams.New(s.node, protocolCommon.Hoarder, protocolCommon.HoarderProtocol); err != nil {
		return nil, fmt.Errorf("new command service failed with: %w", err)
	}

	if s.dbFactory = cfg.Databases(); s.dbFactory == nil {
		s.dbFactory = kvdb.New(s.node)
	}

	if s.db, err = s.dbFactory.New(logger, protocolCommon.Hoarder, 5); err != nil {
		return nil, fmt.Errorf("creating database failed with: %w", err)
	}

	s.setupStreamRoutes()
	s.stream.Start()

	if s.tnsClient, err = tnsApi.New(ctx, clientNode); err != nil {
		return nil, fmt.Errorf("creating new tns client failed with: %w", err)
	}

	if s.stashClient, err = hoarderClient.New(ctx, clientNode); err != nil {
		return nil, fmt.Errorf("creating hoarder fan-out client failed with: %w", err)
	}

	if s.kvStream, err = streamClient.New(clientNode, protocolCommon.HoarderProtocol); err != nil {
		return nil, fmt.Errorf("creating hoarder kvdb replication client failed with: %w", err)
	}

	// Recover what this node already holds, then start membership + reconcile.
	// They share a cancelable context so Close stops them before tearing down the
	// state they read.
	s.recoverClaims(ctx)
	var loopCtx context.Context
	loopCtx, s.reconcileCancel = context.WithCancel(ctx)
	if err = s.startMembership(loopCtx); err != nil {
		return nil, fmt.Errorf("starting membership failed with: %w", err)
	}
	if err = s.startReconcile(loopCtx); err != nil {
		return nil, fmt.Errorf("starting reconcile failed with: %w", err)
	}

	// Adopt TNS-published assets that lack stash claims (see assets.go). Joined
	// by Close via loopsWG like the other loops.
	s.loopsWG.Add(1)
	go func() { defer s.loopsWG.Done(); s.assetSweepLoop(loopCtx) }()

	// Cipher bootstrap (see cipher.go / cipher_ee.go).
	if err = s.cipherInit(ctx, clientNode); err != nil {
		return nil, fmt.Errorf("initializing at-rest cipher failed with: %w", err)
	}

	service = s
	return
}

func (srv *Service) Close() error {
	logger.Info("Closing", protocolCommon.Hoarder)
	defer logger.Info(protocolCommon.Hoarder, "closed")

	// Stop membership + reconcile first so they don't touch state we're tearing
	// down.
	if srv.reconcileCancel != nil {
		srv.reconcileCancel()
	}
	// Join the loops before tearing down the state they read (db, loader,
	// clients): cancel only signals them, a loop may still be mid-iteration.
	srv.loopsWG.Wait()
	if srv.stream != nil {
		srv.stream.Stop()
	}

	// Close per-instance kvdbs before the node's datastore closes under their
	// CRDT goroutines.
	if srv.ldr != nil {
		srv.unloadAll()
	}

	if srv.tnsClient != nil {
		srv.tnsClient.Close()
	}
	if srv.stashClient != nil {
		srv.stashClient.Close()
	}
	if srv.kvStream != nil {
		srv.kvStream.Close()
	}
	if srv.db != nil {
		srv.db.Close()
	}

	return nil
}
