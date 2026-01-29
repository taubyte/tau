package raft

import (
	"context"
	"crypto/cipher"
	"fmt"
	"path"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/streams"
	streamClient "github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	streamService "github.com/taubyte/tau/p2p/streams/service"
)

const (
	// ProtocolRaftPrefix is the prefix for raft stream protocols
	ProtocolRaftPrefix = "/raft/v1"

	cmdSet           = "set"
	cmdGet           = "get"
	cmdDelete        = "delete"
	cmdKeys          = "keys"
	cmdExchangePeers = "exchangePeers"
	cmdJoinVoter     = "joinVoter"

	keyKey       = "key"
	keyValue     = "value"
	keyPrefix    = "prefix"
	keyTimeout   = "timeout"
	keyKeys      = "keys"
	keyFound     = "found"
	keyStartTime = "start"
	keySeenAt    = "seenAt"
	keyPeer      = "peer"
	keyBarrier   = "barrier"
)

// MaxGetHandlerBarrierTimeout is the maximum barrier timeout for Get operations
const MaxGetHandlerBarrierTimeout = 30 * time.Second

type raftStreamService struct {
	cluster          *cluster
	service          streamService.CommandService
	encryptionCipher cipher.AEAD
}

func Protocol(namespace string) string {
	return path.Join(ProtocolRaftPrefix, namespace)
}

func TransportProtocol(namespace string) string {
	return path.Join(ProtocolRaftPrefix, namespace, "transport")
}

func newStreamService(c *cluster) (*raftStreamService, error) {
	protocol := Protocol(c.namespace)

	service, err := streamService.New(c.node, "raft", protocol)
	if err != nil {
		return nil, fmt.Errorf("failed to create stream service: %w", err)
	}

	ss := &raftStreamService{
		cluster:          c,
		service:          service,
		encryptionCipher: c.encryptionCipher,
	}

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

	service.Start()

	return ss, nil
}

func (s *raftStreamService) stop() {
	if s.service != nil {
		s.service.Stop()
	}
}

// handleSet handles set requests
func (s *raftStreamService) handleSet(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	if s.encryptionCipher != nil {
		decryptedBody, err := decryptBody(body, s.encryptionCipher)
		if err != nil {
			return nil, fmt.Errorf("decrypting body: %w", err)
		}
		body = decryptedBody
	}

	key, ok := body[keyKey].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid key")
	}

	value, ok := body[keyValue].([]byte)
	if !ok {
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

	if s.cluster.IsLeader() {
		if err := s.cluster.Set(key, value, timeout); err != nil {
			return nil, err
		}
		resp := cr.Response{"success": true}

		if s.encryptionCipher != nil {
			encryptedResp, err := encryptResponse(resp, s.encryptionCipher)
			if err != nil {
				return nil, fmt.Errorf("encrypting response: %w", err)
			}
			resp = encryptedResp
		}

		return resp, nil
	}

	return s.forwardToLeader(cmdSet, body)
}

// handleGet handles get requests
func (s *raftStreamService) handleGet(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	if s.encryptionCipher != nil {
		decryptedBody, err := decryptBody(body, s.encryptionCipher)
		if err != nil {
			return nil, fmt.Errorf("decrypting body: %w", err)
		}
		body = decryptedBody
	}

	key, ok := body[keyKey].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid key")
	}

	if barrierVal, ok := body[keyBarrier]; ok {
		var barrierNs int64
		switch v := barrierVal.(type) {
		case int64:
			barrierNs = v
		case uint64:
			barrierNs = int64(v)
		case float64:
			barrierNs = int64(v)
		case int:
			barrierNs = int64(v)
		case uint:
			barrierNs = int64(v)
		case int32:
			barrierNs = int64(v)
		case uint32:
			barrierNs = int64(v)
		default:
			return nil, ErrInvalidBarrier
		}
		if barrierNs <= 0 {
			return nil, ErrInvalidBarrier
		}
		barrierTimeout := time.Duration(barrierNs) * time.Nanosecond
		if barrierTimeout > MaxGetHandlerBarrierTimeout {
			return nil, ErrInvalidBarrier
		}
		if err := s.cluster.Barrier(barrierTimeout); err != nil {
			return nil, fmt.Errorf("barrier failed: %w", err)
		}
	}

	val, found := s.cluster.Get(key)
	resp := cr.Response{
		keyValue: val,
		keyFound: found,
	}

	if s.encryptionCipher != nil {
		encryptedResp, err := encryptResponse(resp, s.encryptionCipher)
		if err != nil {
			return nil, fmt.Errorf("encrypting response: %w", err)
		}
		resp = encryptedResp
	}

	return resp, nil
}

// handleDelete handles delete requests
func (s *raftStreamService) handleDelete(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	if s.encryptionCipher != nil {
		decryptedBody, err := decryptBody(body, s.encryptionCipher)
		if err != nil {
			return nil, fmt.Errorf("decrypting body: %w", err)
		}
		body = decryptedBody
	}

	key, ok := body[keyKey].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid key")
	}

	timeout := 5 * time.Second
	if timeoutVal, ok := body[keyTimeout].(float64); ok {
		timeout = time.Duration(timeoutVal) * time.Millisecond
	}

	if s.cluster.IsLeader() {
		if err := s.cluster.Delete(key, timeout); err != nil {
			return nil, err
		}
		resp := cr.Response{"success": true}

		if s.encryptionCipher != nil {
			encryptedResp, err := encryptResponse(resp, s.encryptionCipher)
			if err != nil {
				return nil, fmt.Errorf("encrypting response: %w", err)
			}
			resp = encryptedResp
		}

		return resp, nil
	}

	return s.forwardToLeader(cmdDelete, body)
}

// handleKeys handles keys requests
func (s *raftStreamService) handleKeys(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	if s.encryptionCipher != nil {
		decryptedBody, err := decryptBody(body, s.encryptionCipher)
		if err != nil {
			return nil, fmt.Errorf("decrypting body: %w", err)
		}
		body = decryptedBody
	}

	prefix := ""
	if prefixVal, ok := body[keyPrefix].(string); ok {
		prefix = prefixVal
	}

	keys := s.cluster.Keys(prefix)
	if keys == nil {
		keys = []string{}
	}
	resp := cr.Response{
		keyKeys: keys,
	}

	if s.encryptionCipher != nil {
		encryptedResp, err := encryptResponse(resp, s.encryptionCipher)
		if err != nil {
			return nil, fmt.Errorf("encrypting response: %w", err)
		}
		resp = encryptedResp
	}

	return resp, nil
}

// forwardToLeader forwards a command to the current leader
func (s *raftStreamService) forwardToLeader(cmd string, body command.Body) (cr.Response, error) {
	leader, err := s.cluster.Leader()
	if err != nil {
		return nil, ErrNoLeader
	}

	cli := s.cluster.raftClient.(*client)
	resCh, err := cli.New(cmd,
		streamClient.Body(body),
		streamClient.To(leader),
		streamClient.Threshold(1),
	).Do()
	if err != nil {
		return nil, fmt.Errorf("forwarding to leader failed: %w", err)
	}

	res := <-resCh
	if res == nil {
		return nil, fmt.Errorf("forwarding to leader failed: no responses")
	}
	defer res.Close()

	if err := res.Error(); err != nil {
		return nil, fmt.Errorf("forwarding to leader failed: %w", err)
	}

	return res.Response, nil
}

func (s *raftStreamService) handleExchangePeers(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	if s.encryptionCipher != nil {
		decryptedBody, err := decryptBody(body, s.encryptionCipher)
		if err != nil {
			return nil, fmt.Errorf("decrypting body: %w", err)
		}
		body = decryptedBody
	}

	tracker := s.cluster.tracker

	theirStart := time.UnixMilli(toInt64(body[keyStartTime]))

	if theirSeenAt, ok := body[keySeenAt].(map[string]interface{}); ok {
		theirPeers := make(map[string]int64)
		for k, v := range theirSeenAt {
			theirPeers[k] = toInt64(v)
		}
		newPeers := tracker.mergePeers(theirStart, theirPeers)
		for _, newPeer := range newPeers {
			s.cluster.dialPeer(ctx, newPeer)
		}
	}

	if conn != nil {
		tracker.addPeer(conn.RemotePeer())
	}

	ourStart, ourPeers := tracker.getPeersMap()
	resp := cr.Response{
		keyStartTime: ourStart.UnixMilli(),
		keySeenAt:    ourPeers,
	}

	if s.encryptionCipher != nil {
		encryptedResp, err := encryptResponse(resp, s.encryptionCipher)
		if err != nil {
			return nil, fmt.Errorf("encrypting response: %w", err)
		}
		resp = encryptedResp
	}

	return resp, nil
}

func (s *raftStreamService) handleJoinVoter(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	if s.encryptionCipher != nil {
		decryptedBody, err := decryptBody(body, s.encryptionCipher)
		if err != nil {
			return nil, fmt.Errorf("decrypting body: %w", err)
		}
		body = decryptedBody
	}

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
		resp := cr.Response{"success": true}

		if s.encryptionCipher != nil {
			encryptedResp, err := encryptResponse(resp, s.encryptionCipher)
			if err != nil {
				return nil, fmt.Errorf("encrypting response: %w", err)
			}
			resp = encryptedResp
		}

		return resp, nil
	}

	leaderCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.cluster.WaitForLeader(leaderCtx); err != nil {
		return nil, ErrNoLeader
	}
	if s.cluster.IsLeader() {
		if err := s.cluster.AddVoter(peerID, timeout); err != nil {
			return nil, err
		}
		resp := cr.Response{"success": true}
		if s.encryptionCipher != nil {
			encryptedResp, err := encryptResponse(resp, s.encryptionCipher)
			if err != nil {
				return nil, fmt.Errorf("encrypting response: %w", err)
			}
			resp = encryptedResp
		}
		return resp, nil
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
