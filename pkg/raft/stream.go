package raft

import (
	"context"
	"crypto/cipher"
	"errors"
	"fmt"
	"net/http"
	"path"
	"time"

	"connectrpc.com/connect"
	"github.com/fxamacker/cbor/v2"
	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	discoveryUtil "github.com/libp2p/go-libp2p/p2p/discovery/util"
	"github.com/taubyte/tau/p2p/rpc"
	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2/raftv2connect"
)

var streamLogger = logging.Logger("raft-stream")

// ProtocolRaftPrefix is the protocol id every group's calls and transport
// are found under; a group appends its namespace to it.
var ProtocolRaftPrefix = string(rpc.Protocol(raftv2connect.RaftServiceName))

// defaultApplyTimeout is how long the leader may take to apply a write whose
// request names no timeout.
const defaultApplyTimeout = 5 * time.Second

// MaxGetHandlerBarrierTimeout is the maximum barrier timeout for Get operations
const MaxGetHandlerBarrierTimeout = 30 * time.Second

// raftStreamService answers one group's calls on Protocol(namespace).
type raftStreamService struct {
	cluster *cluster
	server  *rpc.Server
	cancel  context.CancelFunc
}

var _ raftv2connect.RaftServiceHandler = (*raftStreamService)(nil)

func Protocol(namespace string) string {
	return path.Join(ProtocolRaftPrefix, namespace)
}

func TransportProtocol(namespace string) string {
	return path.Join(ProtocolRaftPrefix, namespace, "transport")
}

// codecOptions seals every message with the group's key when it has one.
func codecOptions(gcm cipher.AEAD) (connect.Option, bool) {
	if gcm == nil {
		return nil, false
	}
	return connect.WithCodec(sealedCodec{gcm: gcm}), true
}

func newStreamService(c *cluster) (*raftStreamService, error) {
	protocol := protocol.ID(Protocol(c.namespace))
	ss := &raftStreamService{cluster: c}

	var opts []connect.HandlerOption
	if opt, ok := codecOptions(c.encryptionCipher); ok {
		opts = append(opts, opt)
	}
	route, handler := raftv2connect.NewRaftServiceHandler(ss, opts...)
	mux := http.NewServeMux()
	mux.Handle(route, handler)

	ctx, cancel := context.WithCancel(c.node.Context())
	server, err := rpc.Listen(ctx, c.node.Peer(), protocol, mux)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to listen on %s: %w", protocol, err)
	}
	ss.server = server
	ss.cancel = cancel

	if d := c.node.Discovery(); d != nil {
		discoveryUtil.Advertise(ctx, d, string(protocol))
	}

	return ss, nil
}

func (s *raftStreamService) stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.server != nil {
		s.server.Close()
	}
}

// remotePeer is the caller, or "" when the call did not arrive over a stream
// (a handler invoked directly).
func remotePeer(ctx context.Context) peer.ID {
	pid, err := rpc.RemotePeer(ctx)
	if err != nil {
		return ""
	}
	return pid
}

func applyTimeout(ms int64) time.Duration {
	if ms <= 0 {
		return defaultApplyTimeout
	}
	return time.Duration(ms) * time.Millisecond
}

func (s *raftStreamService) Set(ctx context.Context, req *connect.Request[raftv2.SetRequest]) (*connect.Response[raftv2.SetResponse], error) {
	m := req.Msg
	if m.GetKey() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("missing or invalid key"))
	}
	if m.Value == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("missing or invalid value"))
	}

	if !s.cluster.IsLeader() {
		return forwardToLeader(ctx, s, "set", func(ctx context.Context, c raftv2connect.RaftServiceClient) (*connect.Response[raftv2.SetResponse], error) {
			return c.Set(ctx, connect.NewRequest(m))
		})
	}
	if err := s.cluster.Set(m.GetKey(), m.GetValue(), applyTimeout(m.GetTimeoutMs())); err != nil {
		return nil, err
	}
	return connect.NewResponse(&raftv2.SetResponse{}), nil
}

func (s *raftStreamService) Get(ctx context.Context, req *connect.Request[raftv2.GetRequest]) (*connect.Response[raftv2.GetResponse], error) {
	m := req.Msg
	if m.GetKey() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("missing or invalid key"))
	}

	if m.BarrierNs != nil {
		barrierNs := m.GetBarrierNs()
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

	val, found := s.cluster.Get(m.GetKey())
	return connect.NewResponse(&raftv2.GetResponse{Value: val, Found: found}), nil
}

func (s *raftStreamService) Delete(ctx context.Context, req *connect.Request[raftv2.DeleteRequest]) (*connect.Response[raftv2.DeleteResponse], error) {
	m := req.Msg
	if m.GetKey() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("missing or invalid key"))
	}

	if !s.cluster.IsLeader() {
		return forwardToLeader(ctx, s, "delete", func(ctx context.Context, c raftv2connect.RaftServiceClient) (*connect.Response[raftv2.DeleteResponse], error) {
			return c.Delete(ctx, connect.NewRequest(m))
		})
	}
	if err := s.cluster.Delete(m.GetKey(), applyTimeout(m.GetTimeoutMs())); err != nil {
		return nil, err
	}
	return connect.NewResponse(&raftv2.DeleteResponse{}), nil
}

func (s *raftStreamService) Keys(ctx context.Context, req *connect.Request[raftv2.KeysRequest]) (*connect.Response[raftv2.KeysResponse], error) {
	keys := s.cluster.Keys(req.Msg.GetPrefix())
	if keys == nil {
		keys = []string{}
	}
	return connect.NewResponse(&raftv2.KeysResponse{Keys: keys}), nil
}

// forwardToLeader sends a call this member cannot answer to the current
// leader and returns the leader's answer as its own.
func forwardToLeader[T any](ctx context.Context, s *raftStreamService, what string,
	call func(context.Context, raftv2connect.RaftServiceClient) (*connect.Response[T], error)) (*connect.Response[T], error) {
	leader, err := s.cluster.Leader()
	if err != nil {
		streamLogger.Infof("[%s] cannot forward %q: no leader", s.cluster.node.ID().ShortString(), what)
		return nil, ErrNoLeader
	}

	streamLogger.Infof("[%s] forwarding %q to leader %s", s.cluster.node.ID().ShortString(), what, leader.ShortString())

	cli, ok := s.cluster.raftClient.(*client)
	if !ok {
		return nil, fmt.Errorf("forwarding to leader failed: no client")
	}
	res, err := first(ctx, cli, []peer.ID{leader}, sendToPeerTimeout, call)
	if err != nil {
		return nil, fmt.Errorf("forwarding to leader failed: %w", err)
	}
	return res, nil
}

func (s *raftStreamService) ExchangePeers(ctx context.Context, req *connect.Request[raftv2.ExchangePeersRequest]) (*connect.Response[raftv2.ExchangePeersResponse], error) {
	tracker := s.cluster.tracker
	m := req.Msg

	if m.SeenAt != nil {
		newPeers := tracker.mergePeers(time.UnixMilli(m.GetStartMs()), m.GetSeenAt())
		for _, newPeer := range newPeers {
			s.cluster.dialPeer(ctx, newPeer)
		}
	}

	if from := remotePeer(ctx); from != "" {
		tracker.addPeer(from)
	}

	ourStart, ourPeers := tracker.getPeersMap()
	return connect.NewResponse(&raftv2.ExchangePeersResponse{
		StartMs: ourStart.UnixMilli(),
		SeenAt:  ourPeers,
	}), nil
}

func (s *raftStreamService) JoinVoter(ctx context.Context, req *connect.Request[raftv2.JoinVoterRequest]) (*connect.Response[raftv2.JoinVoterResponse], error) {
	m := req.Msg
	timeout := applyTimeout(m.GetTimeoutMs())

	peerID, err := peerFromRequestOrCaller(ctx, m.GetPeer())
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
		return connect.NewResponse(&raftv2.JoinVoterResponse{}), nil
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
		return connect.NewResponse(&raftv2.JoinVoterResponse{}), nil
	}

	streamLogger.Infof("[%s] joinVoter: forwarding request for %s to leader",
		s.cluster.node.ID().ShortString(), peerID.ShortString())

	forward := &raftv2.JoinVoterRequest{Peer: peerID.String(), TimeoutMs: m.GetTimeoutMs()}
	return forwardToLeader(ctx, s, "joinVoter", func(ctx context.Context, c raftv2connect.RaftServiceClient) (*connect.Response[raftv2.JoinVoterResponse], error) {
		return c.JoinVoter(ctx, connect.NewRequest(forward))
	})
}

func peerFromRequestOrCaller(ctx context.Context, named string) (peer.ID, error) {
	if named != "" {
		return peer.Decode(named)
	}
	if from := remotePeer(ctx); from != "" {
		return from, nil
	}
	return "", fmt.Errorf("missing peer id")
}

// ClusterInfo returns this node's cluster view; does not require leadership.
func (s *raftStreamService) ClusterInfo(ctx context.Context, req *connect.Request[raftv2.ClusterInfoRequest]) (*connect.Response[raftv2.ClusterInfoResponse], error) {
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

	streamLogger.Infof("[%s] clusterInfo requested (from=%s): leader=%s term=%d lastIndex=%d members=%d",
		s.cluster.node.ID().ShortString(), remotePeer(ctx).ShortString(), leaderID, term, lastIdx, memberCount)

	return connect.NewResponse(&raftv2.ClusterInfoResponse{
		Leader:      leaderID,
		Term:        term,
		LastIndex:   lastIdx,
		MemberCount: int64(memberCount),
		NodeId:      s.cluster.node.ID().String(),
	}), nil
}

// ExportFSM serves FSM state from the leader (followers forward).
func (s *raftStreamService) ExportFSM(ctx context.Context, req *connect.Request[raftv2.ExportFSMRequest]) (*connect.Response[raftv2.ExportFSMResponse], error) {
	from := remotePeer(ctx).ShortString()

	if !s.cluster.IsLeader() {
		streamLogger.Infof("[%s] exportFSM requested (from=%s) — forwarding to leader",
			s.cluster.node.ID().ShortString(), from)
		return forwardToLeader(ctx, s, "exportFSM", func(ctx context.Context, c raftv2connect.RaftServiceClient) (*connect.Response[raftv2.ExportFSMResponse], error) {
			return c.ExportFSM(ctx, connect.NewRequest(&raftv2.ExportFSMRequest{}))
		})
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

	return connect.NewResponse(&raftv2.ExportFSMResponse{FsmState: stateBytes, Clock: clock}), nil
}

// HealAck wakes losers blocked in yieldAndRejoin after the winner merged.
func (s *raftStreamService) HealAck(ctx context.Context, req *connect.Request[raftv2.HealAckRequest]) (*connect.Response[raftv2.HealAckResponse], error) {
	fromPeer := remotePeer(ctx)
	streamLogger.Infof("[%s] received healAck from %s — signaling healer",
		s.cluster.node.ID().ShortString(), fromPeer.ShortString())

	if s.cluster.healer != nil {
		s.cluster.healer.signalHealAck(fromPeer)
	}

	return connect.NewResponse(&raftv2.HealAckResponse{}), nil
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
