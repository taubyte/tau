package raft

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	streamService "github.com/taubyte/tau/p2p/streams/service"
)

const (
	// ProtocolRaftPrefix is the prefix for raft stream protocols
	// Full protocol: /raft/v1/<namespace>
	ProtocolRaftPrefix = "/raft/v1"

	// Command names
	cmdSet           = "set"
	cmdGet           = "get"
	cmdDelete        = "delete"
	cmdKeys          = "keys"
	cmdExchangePeers = "exchangePeers"
	cmdJoinVoter     = "joinVoter"

	// Body keys
	keyKey       = "key"
	keyValue     = "value"
	keyPrefix    = "prefix"
	keyTimeout   = "timeout"
	keyKeys      = "keys"
	keyFound     = "found"
	keyStartTime = "start"
	keySeenAt    = "seenAt"
	keyPeer      = "peer"
)

// streamService wraps the cluster with a command service for p2p operations
type raftStreamService struct {
	cluster *cluster
	service streamService.CommandService
}

// Protocol returns the full protocol path for a namespace (for stream commands)
func Protocol(namespace string) string {
	return path.Join(ProtocolRaftPrefix, namespace)
}

// TransportProtocol returns the protocol path for Raft transport RPC
func TransportProtocol(namespace string) string {
	return path.Join(ProtocolRaftPrefix, namespace, "transport")
}

// newStreamService creates a stream service for handling raft commands
func newStreamService(c *cluster) (*raftStreamService, error) {
	protocol := Protocol(c.namespace)

	service, err := streamService.New(c.node, "raft", protocol)
	if err != nil {
		return nil, fmt.Errorf("failed to create stream service: %w", err)
	}

	ss := &raftStreamService{
		cluster: c,
		service: service,
	}

	// Register command handlers
	if err := service.Define(cmdSet, ss.handleSet); err != nil {
		service.Stop()
		return nil, fmt.Errorf("failed to define set handler: %w", err)
	}

	if err := service.Define(cmdGet, ss.handleGet); err != nil {
		service.Stop()
		return nil, fmt.Errorf("failed to define get handler: %w", err)
	}

	if err := service.Define(cmdDelete, ss.handleDelete); err != nil {
		service.Stop()
		return nil, fmt.Errorf("failed to define delete handler: %w", err)
	}

	if err := service.Define(cmdKeys, ss.handleKeys); err != nil {
		service.Stop()
		return nil, fmt.Errorf("failed to define keys handler: %w", err)
	}

	if err := service.Define(cmdExchangePeers, ss.handleExchangePeers); err != nil {
		service.Stop()
		return nil, fmt.Errorf("failed to define exchangePeers handler: %w", err)
	}

	if err := service.Define(cmdJoinVoter, ss.handleJoinVoter); err != nil {
		service.Stop()
		return nil, fmt.Errorf("failed to define joinVoter handler: %w", err)
	}

	// Start accepting connections after all handlers are registered
	service.Start()

	return ss, nil
}

// stop stops the stream service
func (s *raftStreamService) stop() {
	if s.service != nil {
		s.service.Stop()
	}
}

// handleSet handles set requests - forwards to leader if not leader
func (s *raftStreamService) handleSet(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	key, ok := body[keyKey].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid key")
	}

	value, ok := body[keyValue].([]byte)
	if !ok {
		// Try string conversion
		if strVal, ok := body[keyValue].(string); ok {
			value = []byte(strVal)
		} else {
			return nil, fmt.Errorf("missing or invalid value")
		}
	}

	timeout := 5 * time.Second
	if timeoutVal, ok := body[keyTimeout].(float64); ok {
		timeout = time.Duration(timeoutVal) * time.Millisecond
	}

	// If we're the leader, apply directly
	if s.cluster.IsLeader() {
		if err := s.cluster.Set(key, value, timeout); err != nil {
			return nil, err
		}
		return cr.Response{"success": true}, nil
	}

	// Forward to leader
	return s.forwardToLeader(cmdSet, body)
}

// handleGet handles get requests - reads from local state
func (s *raftStreamService) handleGet(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	key, ok := body[keyKey].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid key")
	}

	val, found := s.cluster.Get(key)
	return cr.Response{
		keyValue: val,
		keyFound: found,
	}, nil
}

// handleDelete handles delete requests - forwards to leader if not leader
func (s *raftStreamService) handleDelete(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	key, ok := body[keyKey].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid key")
	}

	timeout := 5 * time.Second
	if timeoutVal, ok := body[keyTimeout].(float64); ok {
		timeout = time.Duration(timeoutVal) * time.Millisecond
	}

	// If we're the leader, apply directly
	if s.cluster.IsLeader() {
		if err := s.cluster.Delete(key, timeout); err != nil {
			return nil, err
		}
		return cr.Response{"success": true}, nil
	}

	// Forward to leader
	return s.forwardToLeader(cmdDelete, body)
}

// handleKeys handles keys requests - reads from local state
func (s *raftStreamService) handleKeys(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	prefix := ""
	if prefixVal, ok := body[keyPrefix].(string); ok {
		prefix = prefixVal
	}

	keys := s.cluster.Keys(prefix)
	if keys == nil {
		keys = []string{}
	}
	return cr.Response{
		keyKeys: keys,
	}, nil
}

// forwardToLeader forwards a command to the current leader
func (s *raftStreamService) forwardToLeader(cmd string, body command.Body) (cr.Response, error) {
	leader, err := s.cluster.Leader()
	if err != nil {
		return nil, ErrNoLeader
	}

	// Use the cluster's raft client to forward
	if s.cluster.raftClient == nil {
		return nil, fmt.Errorf("raft client not initialized")
	}

	return s.cluster.raftClient.Send(cmd, body, leader)
}

// handleExchangePeers handles peer list exchange during bootstrap
func (s *raftStreamService) handleExchangePeers(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	tracker := s.cluster.tracker

	// Get their data
	theirStart := time.UnixMilli(toInt64(body[keyStartTime]))

	// Merge their peers into ours and dial any new ones
	if theirSeenAt, ok := body[keySeenAt].(map[string]interface{}); ok {
		theirPeers := make(map[string]int64)
		for k, v := range theirSeenAt {
			theirPeers[k] = toInt64(v)
		}
		newPeers := tracker.mergePeers(theirStart, theirPeers)
		// Dial newly discovered peers in background
		for _, newPeer := range newPeers {
			s.cluster.dialPeer(ctx, newPeer)
		}
	}

	// Also add the sender if connection is available
	if conn != nil {
		tracker.addPeer(conn.RemotePeer())
	}

	// Return our peer list
	ourStart, ourPeers := tracker.getPeersMap()
	return cr.Response{
		keyStartTime: ourStart.UnixMilli(),
		keySeenAt:    ourPeers,
	}, nil
}

// handleJoinVoter adds the requester as a voting member.
// NOTE: join safeguards to be added.
func (s *raftStreamService) handleJoinVoter(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	timeout := 5 * time.Second
	if timeoutVal, ok := body[keyTimeout].(float64); ok {
		timeout = time.Duration(timeoutVal) * time.Millisecond
	}

	peerID, err := peerFromBodyOrConn(body, conn)
	if err != nil {
		return nil, err
	}

	if s.cluster.IsLeader() {
		if err := s.cluster.AddVoter(peerID, timeout); err != nil {
			return nil, err
		}
		return cr.Response{"success": true}, nil
	}

	leaderCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.cluster.WaitForLeader(leaderCtx); err != nil {
		return nil, ErrNoLeader
	}

	body[keyPeer] = peerID.String()
	resp, err := s.forwardToLeader(cmdJoinVoter, body)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func peerFromBodyOrConn(body command.Body, conn streams.Connection) (peer.ID, error) {
	if peerVal, ok := body[keyPeer].(string); ok && peerVal != "" {
		return peer.Decode(peerVal)
	}
	if conn != nil {
		return conn.RemotePeer(), nil
	}
	return "", fmt.Errorf("missing peer id")
}
