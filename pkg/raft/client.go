package raft

import (
	"context"
	"crypto/cipher"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/fxamacker/cbor/v2"
	"github.com/hashicorp/raft"
	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	taupeer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2/raftv2connect"
)

var clientLogger = logging.Logger("raft-client")

// One call to one peer, dial included; a write adds its own apply timeout.
const sendToPeerTimeout = 10 * time.Second

type Client interface {
	Set(key string, value []byte, timeout time.Duration, peers ...peer.ID) error
	// A barrierNs above 0, at most MaxGetHandlerBarrierTimeout, reads behind a
	// barrier; 0 reads without one; anything else is ErrInvalidBarrier.
	Get(key string, barrierNs int64, peers ...peer.ID) ([]byte, bool, error)
	Delete(key string, timeout time.Duration, peers ...peer.ID) error
	Keys(prefix string, peers ...peer.ID) ([]string, error)
	Close() error
}

type internalClient interface {
	JoinVoter(ctx context.Context, peerID peer.ID, timeout time.Duration, peers ...peer.ID) error
	ExchangePeers(ourStart time.Time, ourPeers map[string]int64, target peer.ID) (time.Time, map[string]int64, error)
	ClusterInfo(target peer.ID) (*ClusterInfoResponse, error)
	ExportFSM(target peer.ID) (map[string]CRDTEntry, uint64, error)
	HealAck(target peer.ID) error
	Close() error
}

// client holds one connection per peer of one group.
type client struct {
	node     taupeer.Node
	protocol protocol.ID
	opts     []connect.ClientOption

	ctx    context.Context
	cancel context.CancelFunc

	mu    sync.Mutex
	peers map[peer.ID]*peerConn
}

type peerConn struct {
	conn *rpc.Client
	svc  raftv2connect.RaftServiceClient
}

func newInternalClient(node taupeer.Node, namespace string, encryptionCipher cipher.AEAD) (internalClient, error) {
	if node == nil {
		return nil, errors.New("no node to call from")
	}
	var opts []connect.ClientOption
	if opt, ok := codecOptions(encryptionCipher); ok {
		opts = append(opts, opt)
	}
	ctx, cancel := context.WithCancel(node.Context())
	return &client{
		node:     node,
		protocol: protocol.ID(Protocol(namespace)),
		opts:     opts,
		ctx:      ctx,
		cancel:   cancel,
		peers:    make(map[peer.ID]*peerConn),
	}, nil
}

func NewClient(node taupeer.Node, namespace string, encryptionCipher cipher.AEAD) (Client, error) {
	cli, err := newInternalClient(node, namespace, encryptionCipher)
	if err != nil {
		return nil, err
	}
	return cli.(*client), nil
}

func (c *client) to(pid peer.ID) (raftv2connect.RaftServiceClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.peers == nil {
		return nil, errors.New("client is closed")
	}
	if pc, ok := c.peers[pid]; ok {
		return pc.svc, nil
	}
	conn, err := rpc.NewClient(c.node.Peer(), pid, c.protocol)
	if err != nil {
		return nil, err
	}
	pc := &peerConn{conn: conn, svc: raftv2connect.NewRaftServiceClient(conn.HTTP(), conn.URL(), c.opts...)}
	c.peers[pid] = pc
	return pc.svc, nil
}

// first calls every peer at once and returns the first success; with every
// peer failing, the first failure, so a refusal (no leader) can be told from an
// unreachable peer.
func first[T any](ctx context.Context, c *client, peers []peer.ID, timeout time.Duration,
	call func(context.Context, raftv2connect.RaftServiceClient) (*connect.Response[T], error)) (*connect.Response[T], error) {
	if len(peers) == 0 {
		return nil, errors.New("no peers to send to")
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type result struct {
		res *connect.Response[T]
		err error
	}
	results := make(chan result, len(peers))
	for _, pid := range peers {
		go func() {
			svc, err := c.to(pid)
			if err != nil {
				results <- result{err: err}
				return
			}
			res, err := call(ctx, svc)
			results <- result{res: res, err: err}
		}()
	}

	var errs []error
	for range peers {
		r := <-results
		if r.err == nil {
			return r.res, nil
		}
		errs = append(errs, sentinel(r.err))
	}
	return nil, errs[0]
}

// sentinel restores the remote errors callers branch on, so errors.Is works
// across the wire.
func sentinel(err error) error {
	for _, s := range []error{ErrNoLeader, ErrInvalidBarrier, ErrNotLeader, raft.ErrLeadershipLost} {
		if strings.Contains(err.Error(), s.Error()) {
			return fmt.Errorf("%w: %v", s, err)
		}
	}
	return err
}

func (c *client) Set(key string, value []byte, timeout time.Duration, peers ...peer.ID) error {
	if value == nil {
		value = []byte{}
	}
	req := &raftv2.SetRequest{Key: key, Value: value, TimeoutMs: timeout.Milliseconds()}
	_, err := first(c.ctx, c, peers, sendToPeerTimeout+timeout, func(ctx context.Context, s raftv2connect.RaftServiceClient) (*connect.Response[raftv2.SetResponse], error) {
		return s.Set(ctx, connect.NewRequest(req))
	})
	if err != nil {
		return fmt.Errorf("set failed: %w", err)
	}
	return nil
}

func (c *client) Get(key string, barrierNs int64, peers ...peer.ID) ([]byte, bool, error) {
	req := &raftv2.GetRequest{Key: key}
	if barrierNs != 0 {
		if barrierNs <= 0 {
			return nil, false, ErrInvalidBarrier
		}
		barrierTimeout := time.Duration(barrierNs) * time.Nanosecond
		if barrierTimeout > MaxGetHandlerBarrierTimeout {
			return nil, false, ErrInvalidBarrier
		}
		req.BarrierNs = &barrierNs
	}

	res, err := first(c.ctx, c, peers, sendToPeerTimeout+time.Duration(barrierNs), func(ctx context.Context, s raftv2connect.RaftServiceClient) (*connect.Response[raftv2.GetResponse], error) {
		return s.Get(ctx, connect.NewRequest(req))
	})
	if err != nil {
		return nil, false, fmt.Errorf("get failed: %w", err)
	}
	if !res.Msg.GetFound() {
		return nil, false, nil
	}
	val := res.Msg.GetValue()
	if val == nil {
		val = []byte{}
	}
	return val, true, nil
}

func (c *client) Delete(key string, timeout time.Duration, peers ...peer.ID) error {
	req := &raftv2.DeleteRequest{Key: key, TimeoutMs: timeout.Milliseconds()}
	_, err := first(c.ctx, c, peers, sendToPeerTimeout+timeout, func(ctx context.Context, s raftv2connect.RaftServiceClient) (*connect.Response[raftv2.DeleteResponse], error) {
		return s.Delete(ctx, connect.NewRequest(req))
	})
	if err != nil {
		return fmt.Errorf("delete failed: %w", err)
	}
	return nil
}

func (c *client) JoinVoter(ctx context.Context, peerID peer.ID, timeout time.Duration, peers ...peer.ID) error {
	clientLogger.Infof("sending joinVoter for %s to %d targets (timeout=%v)",
		peerID.ShortString(), len(peers), timeout)

	req := &raftv2.JoinVoterRequest{Peer: peerID.String(), TimeoutMs: timeout.Milliseconds()}
	_, err := first(ctx, c, peers, sendToPeerTimeout+timeout, func(ctx context.Context, s raftv2connect.RaftServiceClient) (*connect.Response[raftv2.JoinVoterResponse], error) {
		return s.JoinVoter(ctx, connect.NewRequest(req))
	})
	if err != nil {
		if errors.Is(err, ErrNoLeader) {
			clientLogger.Infof("joinVoter for %s failed: no leader available", peerID.ShortString())
			return ErrNoLeader
		}
		clientLogger.Infof("joinVoter for %s failed: %v", peerID.ShortString(), err)
		return fmt.Errorf("join voter failed: %w", err)
	}

	clientLogger.Infof("joinVoter for %s succeeded", peerID.ShortString())
	return nil
}

func (c *client) Keys(prefix string, peers ...peer.ID) ([]string, error) {
	req := &raftv2.KeysRequest{Prefix: prefix}
	res, err := first(c.ctx, c, peers, sendToPeerTimeout, func(ctx context.Context, s raftv2connect.RaftServiceClient) (*connect.Response[raftv2.KeysResponse], error) {
		return s.Keys(ctx, connect.NewRequest(req))
	})
	if err != nil {
		return nil, fmt.Errorf("keys failed: %w", err)
	}
	keys := res.Msg.GetKeys()
	if keys == nil {
		keys = []string{}
	}
	return keys, nil
}

func (c *client) ExchangePeers(ourStart time.Time, ourPeers map[string]int64, target peer.ID) (time.Time, map[string]int64, error) {
	req := &raftv2.ExchangePeersRequest{StartMs: ourStart.UnixMilli(), SeenAt: ourPeers}
	if req.SeenAt == nil {
		req.SeenAt = map[string]int64{}
	}
	res, err := first(c.ctx, c, []peer.ID{target}, sendToPeerTimeout, func(ctx context.Context, s raftv2connect.RaftServiceClient) (*connect.Response[raftv2.ExchangePeersResponse], error) {
		return s.ExchangePeers(ctx, connect.NewRequest(req))
	})
	if err != nil {
		return time.Time{}, nil, err
	}
	theirPeers := res.Msg.GetSeenAt()
	if theirPeers == nil {
		theirPeers = map[string]int64{}
	}
	return time.UnixMilli(res.Msg.GetStartMs()), theirPeers, nil
}

func (c *client) ClusterInfo(target peer.ID) (*ClusterInfoResponse, error) {
	clientLogger.Infof("querying clusterInfo from %s", target.ShortString())

	res, err := first(c.ctx, c, []peer.ID{target}, sendToPeerTimeout, func(ctx context.Context, s raftv2connect.RaftServiceClient) (*connect.Response[raftv2.ClusterInfoResponse], error) {
		return s.ClusterInfo(ctx, connect.NewRequest(&raftv2.ClusterInfoRequest{}))
	})
	if err != nil {
		clientLogger.Infof("clusterInfo from %s failed: %v", target.ShortString(), err)
		return nil, fmt.Errorf("clusterInfo failed: %w", err)
	}

	m := res.Msg
	info := &ClusterInfoResponse{
		LeaderID:    m.GetLeader(),
		Term:        m.GetTerm(),
		LastIndex:   m.GetLastIndex(),
		MemberCount: int(m.GetMemberCount()),
		NodeID:      m.GetNodeId(),
	}

	clientLogger.Infof("clusterInfo from %s: leader=%s term=%d lastIndex=%d members=%d",
		target.ShortString(), info.LeaderID, info.Term, info.LastIndex, info.MemberCount)

	return info, nil
}

func (c *client) ExportFSM(target peer.ID) (map[string]CRDTEntry, uint64, error) {
	clientLogger.Infof("requesting exportFSM from %s", target.ShortString())

	res, err := first(c.ctx, c, []peer.ID{target}, sendToPeerTimeout, func(ctx context.Context, s raftv2connect.RaftServiceClient) (*connect.Response[raftv2.ExportFSMResponse], error) {
		return s.ExportFSM(ctx, connect.NewRequest(&raftv2.ExportFSMRequest{}))
	})
	if err != nil {
		clientLogger.Infof("exportFSM from %s failed: %v", target.ShortString(), err)
		return nil, 0, fmt.Errorf("exportFSM failed: %w", err)
	}

	var state map[string]CRDTEntry
	if err := cbor.Unmarshal(res.Msg.GetFsmState(), &state); err != nil {
		return nil, 0, fmt.Errorf("unmarshaling FSM state: %w", err)
	}

	clientLogger.Infof("exportFSM from %s: %d keys, clock=%d", target.ShortString(), len(state), res.Msg.GetClock())

	return state, res.Msg.GetClock(), nil
}

func (c *client) HealAck(target peer.ID) error {
	clientLogger.Infof("sending healAck to %s", target.ShortString())
	_, err := first(c.ctx, c, []peer.ID{target}, sendToPeerTimeout, func(ctx context.Context, s raftv2connect.RaftServiceClient) (*connect.Response[raftv2.HealAckResponse], error) {
		return s.HealAck(ctx, connect.NewRequest(&raftv2.HealAckRequest{}))
	})
	if err != nil {
		clientLogger.Warnf("healAck to %s failed: %v", target.ShortString(), err)
	}
	return err
}

func (c *client) Close() error {
	c.cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, pc := range c.peers {
		pc.conn.Close()
	}
	c.peers = nil
	return nil
}
