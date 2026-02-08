package monkey

import (
	"context"
	"fmt"
	"path"
	"slices"
	"time"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/clients/p2p/hoarder"
	tnsClient "github.com/taubyte/tau/clients/p2p/tns"
	seerIface "github.com/taubyte/tau/core/services/seer"
	ci "github.com/taubyte/tau/pkg/containers/gc"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	tauConfig "github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/pkg/raft"
	patrickSpecs "github.com/taubyte/tau/pkg/specs/patrick"

	streams "github.com/taubyte/tau/p2p/streams/service"
	protocolCommon "github.com/taubyte/tau/services/common"
)

var logger = log.Logger("tau.monkey.service")

func (srv *Service) subscribe() error {
	return srv.node.PubSubSubscribe(
		patrickSpecs.PubSubIdent,
		func(msg *pubsub.Message) {
			go srv.pubsubMsgHandler(msg)
		},
		func(err error) {
			if err.Error() != "context canceled" {
				logger.Error("Subscription had an error:", err.Error())
				if err := srv.subscribe(); err != nil {
					logger.Error("resubscribe failed with:", err.Error())
				}
			}
		},
	)
}

func New(ctx context.Context, cfg tauConfig.Config) (*Service, error) {
	var err error
	srv := &Service{
		ctx:     ctx,
		dev:     cfg.DevMode(),
		config:  cfg,
		cluster: cfg.Cluster(),
	}
	if srv.cluster == "" {
		srv.cluster = "main"
	}

	err = ci.Start(ctx, ci.Interval(ci.DefaultInterval), ci.MaxAge(ci.DefaultMaxAge))
	if err != nil {
		return nil, err
	}

	if srv.node = cfg.Node(); srv.node == nil {
		srv.node, err = tauConfig.NewLiteNode(ctx, cfg, path.Join(cfg.Root(), protocolCommon.Monkey))
		if err != nil {
			return nil, err
		}
	} else {
		srv.dvPublicKey = cfg.DomainValidation().PublicKey
	}

	srv.clientNode = srv.node
	if cfg.ClientNode() != nil {
		srv.clientNode = cfg.ClientNode()
	}

	if srv.stream, err = streams.New(srv.node, protocolCommon.Monkey, protocolCommon.MonkeyProtocol); err != nil {
		return nil, err
	}
	srv.setupStreamRoutes()
	srv.stream.Start()
	var sc seerIface.Client
	if sc, err = seerClient.New(ctx, srv.clientNode, cfg.SensorsRegistry()); err != nil {
		return nil, fmt.Errorf("creating seer client failed with %s", err)
	}
	if err = protocolCommon.StartSeerBeacon(cfg, sc, seerIface.ServiceTypeMonkey); err != nil {
		return nil, fmt.Errorf("starting seer beacon failed with %s", err)
	}
	srv.monkeys = make(map[string]*Monkey, 0)
	srv.recvJobs = make(map[string]time.Time, 0)
	if srv.patrickClient, err = NewPatrick(ctx, srv.clientNode); err != nil {
		return nil, err
	}
	go srv.pollJobs()
	if srv.tnsClient, err = tnsClient.New(ctx, srv.clientNode); err != nil {
		return nil, err
	}
	if srv.hoarderClient, err = hoarder.New(ctx, srv.clientNode); err != nil {
		return nil, err
	}

	return srv, nil
}

// discoverPatrickPeers returns peer IDs that support both /raft/v1/<cluster> and /patrick/v1 when available.
// If no such peers exist (e.g. dream without RaftCluster), falls back to peers that support only /patrick/v1.
// Includes self when Patrick runs on the same node (same process or same peer ID).
func (srv *Service) discoverPatrickPeers() []peer.ID {
	raftProto := protocol.ID(raft.Protocol(srv.cluster))
	patrickProto := protocol.ID(protocolCommon.PatrickProtocol)
	var out, patrickOnly []peer.ID
	seen := make(map[peer.ID]struct{})
	addIfPatrick := func(pid peer.ID) {
		if len(pid) == 0 {
			return
		}
		if _, ok := seen[pid]; ok {
			return
		}
		protos, err := srv.node.Peer().Peerstore().GetProtocols(pid)
		if err != nil {
			return
		}
		hasRaft := slices.Contains(protos, raftProto)
		hasPatrick := slices.Contains(protos, patrickProto)
		if hasRaft && hasPatrick {
			out = append(out, pid)
			seen[pid] = struct{}{}
		} else if hasPatrick {
			patrickOnly = append(patrickOnly, pid)
			seen[pid] = struct{}{}
		}
	}
	for _, pid := range srv.node.Peer().Peerstore().Peers() {
		addIfPatrick(pid)
	}
	addIfPatrick(srv.node.ID())
	if len(out) == 0 && len(patrickOnly) > 0 {
		out = patrickOnly
	}
	if len(out) == 0 {
		out = append(out, srv.node.ID())
	}
	return out
}

const (
	pollJobInterval     = 5 * time.Second
	pollJobBackoffEmpty = 2 * time.Second
)

func (srv *Service) pollJobs() {
	backoff := pollJobInterval
	for {
		select {
		case <-srv.ctx.Done():
			return
		default:
		}
		peers := srv.discoverPatrickPeers()
		if len(peers) == 0 {
			time.Sleep(pollJobBackoffEmpty)
			continue
		}
		client := srv.patrickClient.Peers(peers...)
		id, jobBytes, err := client.Dequeue()
		if err != nil {
			logger.Errorf("dequeue failed: %v", err)
			time.Sleep(backoff)
			continue
		}
		if id == "" || len(jobBytes) == 0 {
			time.Sleep(pollJobBackoffEmpty)
			continue
		}
		srv.RunJobFromBytes(jobBytes)
		backoff = pollJobInterval
	}
}

func (srv *Service) Close() error {
	logger.Info("Closing", protocolCommon.Monkey)
	defer logger.Info(protocolCommon.Monkey, "closed")

	srv.stream.Stop()

	srv.tnsClient.Close()
	srv.patrickClient.Close()

	return nil
}
