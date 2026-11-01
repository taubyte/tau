// Package swarm is how the copies of a replicated `sql` database reach each
// other across a node's own swarm: musql's replication.Transport over the
// node's libp2p host and its pubsub router.
//
// # One service and one router for every database
//
// A node holds many databases and has one host. So anti-entropy is ONE
// ConnectRPC service (tau.sqlsync.v2, generated from pkg/proto) whose every
// request names its database, and a write's low-latency delivery is a message on
// the database's own pubsub topic, joined on the router the node already runs.
// A protocol or a router per database would be a stream handler or a gossip
// mesh per database on one host.
//
// # Who a database's peers are
//
// The nodes subscribed to its topic. Those are exactly the nodes that have the
// database open, which is the set that can answer for it.
package swarm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"connectrpc.com/connect"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/samyfodil/musql/replication"
	"google.golang.org/protobuf/proto"

	"github.com/taubyte/tau/p2p/rpc"
	sqlsyncv2 "github.com/taubyte/tau/pkg/proto/gen/tau/sqlsync/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/sqlsync/v2/sqlsyncv2connect"
)

// Protocol is the stream protocol the anti-entropy service answers on.
var Protocol = rpc.Protocol(sqlsyncv2connect.SQLSyncServiceName)

// PubSub is the slice of a pubsub router this package needs: joining a topic.
// *pubsub.PubSub satisfies it, and so does the node's.
type PubSub interface {
	Join(topic string, opts ...pubsub.TopicOpt) (*pubsub.Topic, error)
}

// ErrUnknownDatabase is a request for a database this node does not hold open.
var ErrUnknownDatabase = errors.New("swarm: this node holds no such database")

// Mesh is one node's side of every replicated sql database it holds.
type Mesh struct {
	host host.Host
	ps   PubSub

	mu      sync.Mutex
	stores  map[string]*replication.Store
	clients map[peer.ID]sqlsyncv2connect.SQLSyncServiceClient

	server *rpc.Server
}

// New serves the anti-entropy service on h and joins databases' topics on ps.
func New(ctx context.Context, h host.Host, ps PubSub) (*Mesh, error) {
	if h == nil || ps == nil {
		return nil, errors.New("swarm: a mesh needs a host and a pubsub router")
	}
	m := &Mesh{
		host:    h,
		ps:      ps,
		stores:  map[string]*replication.Store{},
		clients: map[peer.ID]sqlsyncv2connect.SQLSyncServiceClient{},
	}
	path, handler := sqlsyncv2connect.NewSQLSyncServiceHandler(&service{m: m})
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server, err := rpc.Listen(ctx, h, Protocol, mux)
	if err != nil {
		return nil, fmt.Errorf("swarm: listening: %w", err)
	}
	m.server = server
	return m, nil
}

// Close stops answering. Databases still open keep their topics until closed.
func (m *Mesh) Close() error { return m.server.Close() }

// Sync is the replication option that syncs one database, named by topic, over
// this mesh. The engine hands it each database's own topic.
func (m *Mesh) Sync(topic string) replication.Option {
	return replication.WithTransport(func(ctx context.Context, s *replication.Syncer) (replication.Transport, error) {
		return m.attach(ctx, topic, s.Store())
	})
}

// attach joins the database's topic and files its store for the service to
// answer from, until ctx — which the database's Close cancels — ends.
func (m *Mesh) attach(ctx context.Context, topic string, store *replication.Store) (*transport, error) {
	// ONE OPEN PER DATABASE PER NODE is musql's to enforce, and it does: a
	// second Open of one file is refused by its site before a transport is
	// asked for, so a topic is never joined twice on this router.
	m.mu.Lock()
	m.stores[topic] = store
	m.mu.Unlock()

	forget := func() {
		m.mu.Lock()
		delete(m.stores, topic)
		m.mu.Unlock()
	}

	t, err := m.ps.Join(topic)
	if err != nil {
		forget()
		return nil, fmt.Errorf("swarm: joining %s: %w", topic, err)
	}
	sub, err := t.Subscribe()
	if err != nil {
		t.Close()
		forget()
		return nil, fmt.Errorf("swarm: subscribing to %s: %w", topic, err)
	}

	tr := &transport{m: m, database: topic, topic: t, sub: sub, ctx: ctx}
	go func() {
		<-ctx.Done()
		sub.Cancel()
		t.Close()
		forget()
	}()
	return tr, nil
}

// client is the anti-entropy client for one peer, opened once.
func (m *Mesh) client(id peer.ID) (sqlsyncv2connect.SQLSyncServiceClient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.clients[id]; ok {
		return c, nil
	}
	conn, err := rpc.NewClient(m.host, id, Protocol)
	if err != nil {
		return nil, err
	}
	c := sqlsyncv2connect.NewSQLSyncServiceClient(conn.HTTP(), conn.URL())
	m.clients[id] = c
	return c, nil
}

func (m *Mesh) store(database string) (*replication.Store, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.stores[database]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("%w: %s", ErrUnknownDatabase, database))
	}
	return s, nil
}

// transport is one database's replication.Transport.
type transport struct {
	m        *Mesh
	database string
	topic    *pubsub.Topic
	sub      *pubsub.Subscription
	ctx      context.Context
}

var _ replication.Transport = (*transport)(nil)

func (t *transport) Peers() []replication.PeerID {
	var out []replication.PeerID
	for _, p := range t.topic.ListPeers() {
		out = append(out, replication.PeerID(p.String()))
	}
	return out
}

func (t *transport) clientFor(p replication.PeerID) (sqlsyncv2connect.SQLSyncServiceClient, error) {
	id, err := peer.Decode(string(p))
	if err != nil {
		return nil, fmt.Errorf("swarm: %q is not a peer: %w", p, err)
	}
	return t.m.client(id)
}

func (t *transport) ExchangeVV(ctx context.Context, p replication.PeerID, vv replication.VersionVector) (replication.VersionVector, error) {
	c, err := t.clientFor(p)
	if err != nil {
		return nil, err
	}
	res, err := c.ExchangeVV(ctx, connect.NewRequest(&sqlsyncv2.ExchangeVVRequest{Database: t.database, Vv: vvToPB(vv)}))
	if err != nil {
		return nil, err
	}
	return vvFromPB(res.Msg.GetVv()), nil
}

func (t *transport) GetOps(ctx context.Context, p replication.PeerID, site string, fromSeq uint64, limit int) ([]replication.Op, error) {
	c, err := t.clientFor(p)
	if err != nil {
		return nil, err
	}
	res, err := c.GetOps(ctx, connect.NewRequest(&sqlsyncv2.GetOpsRequest{
		Database: t.database, Site: site, FromSeq: fromSeq, Limit: uint32(limit),
	}))
	if err != nil {
		return nil, err
	}
	if res.Msg.GetPruned() {
		return nil, replication.ErrPruned
	}
	return opsFromPB(res.Msg.GetOps()), nil
}

func (t *transport) ExchangeAcks(ctx context.Context, p replication.PeerID, acks replication.Acks) (replication.Acks, error) {
	c, err := t.clientFor(p)
	if err != nil {
		return nil, err
	}
	res, err := c.ExchangeAcks(ctx, connect.NewRequest(&sqlsyncv2.ExchangeAcksRequest{Database: t.database, Acks: acksToPB(acks)}))
	if err != nil {
		return nil, err
	}
	return acksFromPB(res.Msg.GetAcks()), nil
}

func (t *transport) GetSnapshot(ctx context.Context, p replication.PeerID, part int, cursor []byte) (replication.SnapshotPage, error) {
	c, err := t.clientFor(p)
	if err != nil {
		return replication.SnapshotPage{}, err
	}
	res, err := c.GetSnapshot(ctx, connect.NewRequest(&sqlsyncv2.GetSnapshotRequest{
		Database: t.database, Part: uint32(part), Cursor: cursor,
	}))
	if err != nil {
		return replication.SnapshotPage{}, err
	}
	return pageFromPB(res.Msg), nil
}

// PushOp publishes op on the database's topic; gossip is topic-wide, so the peer is
// ignored. The publish is detached: libp2p's Publish can wedge during router teardown
// and would block Close.
func (t *transport) PushOp(ctx context.Context, _ replication.PeerID, op replication.Op) error {
	data, err := proto.Marshal(&sqlsyncv2.Push{Op: opToPB(op)})
	if err != nil {
		return fmt.Errorf("swarm: encoding a push: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- t.topic.Publish(ctx, data) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-t.ctx.Done():
		return t.ctx.Err()
	}
}

// SubscribePush delivers every op another node published on the topic.
func (t *transport) SubscribePush(ctx context.Context) (<-chan replication.PeerOp, error) {
	out := make(chan replication.PeerOp, 64)
	self := t.m.host.ID()
	go func() {
		defer close(out)
		for {
			msg, err := t.sub.Next(ctx)
			if err != nil {
				return
			}
			if msg.ReceivedFrom == self {
				continue
			}
			var push sqlsyncv2.Push
			if proto.Unmarshal(msg.Data, &push) != nil || push.GetOp() == nil {
				continue // not ours to apply; a malformed push is dropped, anti-entropy recovers it
			}
			select {
			case out <- replication.PeerOp{From: replication.PeerID(msg.ReceivedFrom.String()), Op: opFromPB(push.GetOp())}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// service answers every database this node holds open.
type service struct{ m *Mesh }

var _ sqlsyncv2connect.SQLSyncServiceHandler = (*service)(nil)

func (s *service) ExchangeVV(_ context.Context, req *connect.Request[sqlsyncv2.ExchangeVVRequest]) (*connect.Response[sqlsyncv2.ExchangeVVResponse], error) {
	store, err := s.m.store(req.Msg.GetDatabase())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&sqlsyncv2.ExchangeVVResponse{Vv: vvToPB(store.LocalVV())}), nil
}

func (s *service) GetOps(ctx context.Context, req *connect.Request[sqlsyncv2.GetOpsRequest]) (*connect.Response[sqlsyncv2.GetOpsResponse], error) {
	store, err := s.m.store(req.Msg.GetDatabase())
	if err != nil {
		return nil, err
	}
	ops, err := store.OpsSince(ctx, req.Msg.GetSite(), req.Msg.GetFromSeq(), int(req.Msg.GetLimit()))
	if errors.Is(err, replication.ErrPruned) {
		return connect.NewResponse(&sqlsyncv2.GetOpsResponse{Pruned: true}), nil
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&sqlsyncv2.GetOpsResponse{Ops: opsToPB(ops)}), nil
}

func (s *service) ExchangeAcks(_ context.Context, req *connect.Request[sqlsyncv2.ExchangeAcksRequest]) (*connect.Response[sqlsyncv2.ExchangeAcksResponse], error) {
	store, err := s.m.store(req.Msg.GetDatabase())
	if err != nil {
		return nil, err
	}
	store.MergeAcks(acksFromPB(req.Msg.GetAcks()))
	return connect.NewResponse(&sqlsyncv2.ExchangeAcksResponse{Acks: acksToPB(store.Acks())}), nil
}

func (s *service) GetSnapshot(ctx context.Context, req *connect.Request[sqlsyncv2.GetSnapshotRequest]) (*connect.Response[sqlsyncv2.GetSnapshotResponse], error) {
	store, err := s.m.store(req.Msg.GetDatabase())
	if err != nil {
		return nil, err
	}
	page, err := store.SnapshotPage(ctx, int(req.Msg.GetPart()), req.Msg.GetCursor())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(pageToPB(page)), nil
}
