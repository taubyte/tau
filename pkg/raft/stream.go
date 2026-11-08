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
	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	discoveryUtil "github.com/libp2p/go-libp2p/p2p/discovery/util"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/codec"
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

const MaxGetHandlerBarrierTimeout = 30 * time.Second

type raftStreamService struct {
	cluster *cluster
	server  *rpc.Server
	cancel  context.CancelFunc
}

var _ raftv2connect.RaftServiceHandler = (*raftStreamService)(nil)

func Protocol(namespace string) string {
	return path.Join(ProtocolRaftPrefix, namespace)
}

// codecOption is the group's codec: tau's own, sealed under the group's key when
// it has one.
func codecOption(gcm cipher.AEAD) connect.Option {
	if gcm == nil {
		return codec.Option()
	}
	return connect.WithCodec(sealedCodec{gcm: gcm})
}

func newStreamService(c *cluster) (*raftStreamService, error) {
	protocol := protocol.ID(Protocol(c.namespace))
	ss := &raftStreamService{cluster: c}

	route, handler := raftv2connect.NewRaftServiceHandler(ss, codecOption(c.encryptionCipher))
	mux := http.NewServeMux()
	mux.Handle(route, handler)

	ctx, cancel := context.WithCancel(c.Context())
	server, err := rpc.Listen(ctx, c.Host(), protocol, mux)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to listen on %s: %w", protocol, err)
	}
	ss.server = server
	ss.cancel = cancel

	if d := c.Discovery(); d != nil {
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

func forwardToLeader[T any](ctx context.Context, s *raftStreamService, what string,
	call func(context.Context, raftv2connect.RaftServiceClient) (*connect.Response[T], error)) (*connect.Response[T], error) {
	leader, err := s.cluster.Leader()
	if err != nil {
		streamLogger.Infof("[%s] cannot forward %q: no leader", s.cluster.ID().ShortString(), what)
		return nil, ErrNoLeader
	}

	streamLogger.Infof("[%s] forwarding %q to leader %s", s.cluster.ID().ShortString(), what, leader.ShortString())

	cli, ok := s.cluster.current().raftClient.(*client)
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
	tracker := s.cluster.current().tracker
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
		s.cluster.ID().ShortString(), peerID.ShortString(), s.cluster.IsLeader())

	budget, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		if err := s.awaitLeader(budget); err != nil {
			streamLogger.Infof("[%s] joinVoter: no leader available to handle %s",
				s.cluster.ID().ShortString(), peerID.ShortString())
			return nil, err
		}
		if !s.cluster.IsLeader() {
			break
		}
		err := s.cluster.AddVoter(peerID, timeout)
		if err == nil {
			return connect.NewResponse(&raftv2.JoinVoterResponse{}), nil
		}
		streamLogger.Warnf("[%s] joinVoter: AddVoter %s failed: %v",
			s.cluster.ID().ShortString(), peerID.ShortString(), err)
		// A leader that lost the lead while adding is between leaders now.
		if !leadershipMoved(err) || budget.Err() != nil {
			return nil, err
		}
	}

	streamLogger.Infof("[%s] joinVoter: forwarding request for %s to leader",
		s.cluster.ID().ShortString(), peerID.ShortString())

	forward := &raftv2.JoinVoterRequest{Peer: peerID.String(), TimeoutMs: m.GetTimeoutMs()}
	return forwardToLeader(ctx, s, "joinVoter", func(ctx context.Context, c raftv2connect.RaftServiceClient) (*connect.Response[raftv2.JoinVoterResponse], error) {
		return c.JoinVoter(ctx, connect.NewRequest(forward))
	})
}

// awaitLeader returns once this member knows a leader, maybe itself. A member
// of a group, or one let in before its configuration reached it, waits within
// ctx for the next; one in no group has no election to wait for and says so at
// once.
func (s *raftStreamService) awaitLeader(ctx context.Context) error {
	if _, err := s.cluster.Leader(); err == nil {
		return nil
	}
	m := s.cluster.current()
	if members, err := m.members(); err != nil || (len(members) == 0 && !m.joined.Load()) {
		return ErrNoLeader
	}
	if s.cluster.WaitForLeader(ctx) != nil {
		return ErrNoLeader
	}
	return nil
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

func (s *raftStreamService) ClusterInfo(ctx context.Context, req *connect.Request[raftv2.ClusterInfoRequest]) (*connect.Response[raftv2.ClusterInfoResponse], error) {
	m := s.cluster.current()
	leaderID := ""
	if leader, err := leaderKnownTo(m.raft); err == nil {
		leaderID = leader.String()
	}

	term := m.raft.Term()
	lastIdx := m.raft.LastIndex()

	memberCount := 0
	if members, err := m.members(); err == nil {
		memberCount = len(members)
	}

	streamLogger.Infof("[%s] clusterInfo requested (from=%s): leader=%s term=%d lastIndex=%d members=%d",
		s.cluster.ID().ShortString(), remotePeer(ctx).ShortString(), leaderID, term, lastIdx, memberCount)

	return connect.NewResponse(&raftv2.ClusterInfoResponse{
		Leader:      leaderID,
		Term:        term,
		LastIndex:   lastIdx,
		MemberCount: int64(memberCount),
		NodeId:      s.cluster.ID().String(),
	}), nil
}

// ExportFSM serves the group's state: a follower that knows its leader
// forwards to it, and a member that knows no leader, an orphan, serves its
// own, which is the state the healer absorbing it has to merge.
func (s *raftStreamService) ExportFSM(ctx context.Context, req *connect.Request[raftv2.ExportFSMRequest]) (*connect.Response[raftv2.ExportFSMResponse], error) {
	from := remotePeer(ctx).ShortString()

	if !s.cluster.IsLeader() {
		if _, err := s.cluster.Leader(); err == nil {
			streamLogger.Infof("[%s] exportFSM requested (from=%s) — forwarding to leader",
				s.cluster.ID().ShortString(), from)
			return forwardToLeader(ctx, s, "exportFSM", func(ctx context.Context, c raftv2connect.RaftServiceClient) (*connect.Response[raftv2.ExportFSMResponse], error) {
				return c.ExportFSM(ctx, connect.NewRequest(&raftv2.ExportFSMRequest{}))
			})
		}
	}

	streamLogger.Infof("[%s] exportFSM requested (from=%s) — serving its own state",
		s.cluster.ID().ShortString(), from)

	m := s.cluster.current()
	state, err := m.fsm.ExportState()
	if err != nil {
		return nil, fmt.Errorf("exporting FSM state: %w", err)
	}

	fsm := m.fsm.(*kvFSM)
	fsm.mu.RLock()
	clock := fsm.clock
	fsm.mu.RUnlock()

	streamLogger.Infof("[%s] exportFSM: %d keys, clock=%d", s.cluster.ID().ShortString(), len(state), clock)

	return connect.NewResponse(&raftv2.ExportFSMResponse{Entries: entriesProto(state), Clock: clock}), nil
}

// HealAck wakes losers blocked in yieldAndRejoin after the winner merged.
func (s *raftStreamService) HealAck(ctx context.Context, req *connect.Request[raftv2.HealAckRequest]) (*connect.Response[raftv2.HealAckResponse], error) {
	fromPeer := remotePeer(ctx)
	streamLogger.Infof("[%s] received healAck from %s — signaling healer",
		s.cluster.ID().ShortString(), fromPeer.ShortString())

	if h := s.cluster.current().healer.Load(); h != nil {
		h.signalHealAck(fromPeer)
	}

	return connect.NewResponse(&raftv2.HealAckResponse{}), nil
}
