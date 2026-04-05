package raft

import (
	"context"
	"crypto/cipher"
	"fmt"
	"path"
	"time"

	"github.com/fxamacker/cbor/v2"
	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/streams"
	streamClient "github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	streamService "github.com/taubyte/tau/p2p/streams/service"
)

var streamLogger = logging.Logger("raft-stream")

const (
	// ProtocolRaftPrefix is the prefix for raft stream protocols
	ProtocolRaftPrefix = "/raft/v1"

	cmdSet           = "set"
	cmdGet           = "get"
	cmdDelete        = "delete"
	cmdKeys          = "keys"
	cmdExchangePeers = "exchangePeers"
	cmdJoinVoter     = "joinVoter"
	cmdClusterInfo   = "clusterInfo"
	cmdExportFSM     = "exportFSM"
	cmdHealAck       = "healAck"

	keyKey         = "key"
	keyValue       = "value"
	keyPrefix      = "prefix"
	keyTimeout     = "timeout"
	keyKeys        = "keys"
	keyFound       = "found"
	keyStartTime   = "start"
	keySeenAt      = "seenAt"
	keyPeer        = "peer"
	keyBarrier     = "barrier"
	keyLeader      = "leader"
	keyTerm        = "term"
	keyLastIndex   = "lastIndex"
	keyMemberCount = "memberCount"
	keyNodeID      = "nodeID"
	keyFSMState    = "fsmState"
	keyClock       = "clock"
	keySuccess     = "success"
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

	if err := service.Define(cmdClusterInfo, ss.handleClusterInfo); err != nil {
		service.Stop()
		return nil, fmt.Errorf("failed to define clusterInfo handler: %w", err)
	}

	if err := service.Define(cmdExportFSM, ss.handleExportFSM); err != nil {
		service.Stop()
		return nil, fmt.Errorf("failed to define exportFSM handler: %w", err)
	}

	if err := service.Define(cmdHealAck, ss.handleHealAck); err != nil {
		service.Stop()
		return nil, fmt.Errorf("failed to define healAck handler: %w", err)
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
		streamLogger.Infof("[%s] cannot forward %q: no leader", s.cluster.node.ID().ShortString(), cmd)
		return nil, ErrNoLeader
	}

	streamLogger.Infof("[%s] forwarding %q to leader %s", s.cluster.node.ID().ShortString(), cmd, leader.ShortString())

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

	streamLogger.Infof("[%s] received joinVoter request from %s (is_leader=%v)",
		s.cluster.node.ID().ShortString(), peerID.ShortString(), s.cluster.IsLeader())

	if s.cluster.IsLeader() {
		if err := s.cluster.AddVoter(peerID, timeout); err != nil {
			streamLogger.Warnf("[%s] joinVoter: AddVoter %s failed: %v",
				s.cluster.node.ID().ShortString(), peerID.ShortString(), err)
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
		streamLogger.Infof("[%s] joinVoter: no leader available to handle %s",
			s.cluster.node.ID().ShortString(), peerID.ShortString())
		return nil, ErrNoLeader
	}
	if s.cluster.IsLeader() {
		if err := s.cluster.AddVoter(peerID, timeout); err != nil {
			streamLogger.Warnf("[%s] joinVoter: AddVoter %s failed (after becoming leader): %v",
				s.cluster.node.ID().ShortString(), peerID.ShortString(), err)
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

	streamLogger.Infof("[%s] joinVoter: forwarding request for %s to leader",
		s.cluster.node.ID().ShortString(), peerID.ShortString())

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

// handleClusterInfo returns this node's cluster view; does not require leadership.
func (s *raftStreamService) handleClusterInfo(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	if s.encryptionCipher != nil {
		decryptedBody, err := decryptBody(body, s.encryptionCipher)
		if err != nil {
			return nil, fmt.Errorf("decrypting body: %w", err)
		}
		body = decryptedBody
	}

	leaderID := ""
	if leader, err := s.cluster.Leader(); err == nil {
		leaderID = leader.String()
	}

	var term uint64
	var lastIdx uint64
	if s.cluster.raft != nil {
		stats := s.cluster.raft.Stats()
		term = parseUint64(stats["term"])
		lastIdx = parseUint64(stats["last_log_index"])
	}

	memberCount := 0
	if members, err := s.cluster.Members(); err == nil {
		memberCount = len(members)
	}

	from := ""
	if conn != nil {
		from = conn.RemotePeer().ShortString()
	}
	streamLogger.Infof("[%s] clusterInfo requested (from=%s): leader=%s term=%d lastIndex=%d members=%d",
		s.cluster.node.ID().ShortString(), from, leaderID, term, lastIdx, memberCount)

	resp := cr.Response{
		keyLeader:      leaderID,
		keyTerm:        term,
		keyLastIndex:   lastIdx,
		keyMemberCount: memberCount,
		keyNodeID:      s.cluster.node.ID().String(),
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

// handleExportFSM serves FSM state from the leader (followers forward).
func (s *raftStreamService) handleExportFSM(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	if s.encryptionCipher != nil {
		decryptedBody, err := decryptBody(body, s.encryptionCipher)
		if err != nil {
			return nil, fmt.Errorf("decrypting body: %w", err)
		}
		body = decryptedBody
	}

	from := ""
	if conn != nil {
		from = conn.RemotePeer().ShortString()
	}

	if !s.cluster.IsLeader() {
		streamLogger.Infof("[%s] exportFSM requested (from=%s) — forwarding to leader",
			s.cluster.node.ID().ShortString(), from)
		return s.forwardToLeader(cmdExportFSM, body)
	}

	streamLogger.Infof("[%s] exportFSM requested (from=%s) — serving as leader",
		s.cluster.node.ID().ShortString(), from)

	state, err := s.cluster.fsm.ExportState()
	if err != nil {
		return nil, fmt.Errorf("exporting FSM state: %w", err)
	}

	stateBytes, err := cbor.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("marshaling FSM state: %w", err)
	}

	fsm := s.cluster.fsm.(*kvFSM)
	fsm.mu.RLock()
	clock := fsm.clock
	fsm.mu.RUnlock()

	streamLogger.Infof("[%s] exportFSM: %d keys, clock=%d", s.cluster.node.ID().ShortString(), len(state), clock)

	resp := cr.Response{
		keyFSMState: stateBytes,
		keyClock:    clock,
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

// handleHealAck wakes losers blocked in yieldAndRejoin after the winner merged.
func (s *raftStreamService) handleHealAck(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	if s.encryptionCipher != nil {
		decryptedBody, err := decryptBody(body, s.encryptionCipher)
		if err != nil {
			return nil, fmt.Errorf("decrypting body: %w", err)
		}
		body = decryptedBody
	}

	var fromPeer peer.ID
	if conn != nil {
		fromPeer = conn.RemotePeer()
	}
	streamLogger.Infof("[%s] received healAck from %s — signaling healer",
		s.cluster.node.ID().ShortString(), fromPeer.ShortString())

	if s.cluster.healer != nil {
		s.cluster.healer.signalHealAck(fromPeer)
	}

	resp := cr.Response{keySuccess: true}

	if s.encryptionCipher != nil {
		encryptedResp, err := encryptResponse(resp, s.encryptionCipher)
		if err != nil {
			return nil, fmt.Errorf("encrypting response: %w", err)
		}
		resp = encryptedResp
	}

	return resp, nil
}

func parseUint64(s string) uint64 {
	var n uint64
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + uint64(c-'0')
	}
	return n
}
