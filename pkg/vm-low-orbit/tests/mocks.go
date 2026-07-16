package tests

import (
	"context"
	"sync"

	cid "github.com/ipfs/go-cid"
	logging "github.com/ipfs/go-log/v2"
	dbIface "github.com/taubyte/tau/core/services/substrate/components/database"
	p2pIface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	psIface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	res "github.com/taubyte/tau/p2p/streams/command/response"
	kvdbMock "github.com/taubyte/tau/pkg/kvdb/mock"
	dbkv "github.com/taubyte/tau/services/substrate/components/database/kv"
)

// Backend mocks. Each embeds the real interface (nil) and overrides only the
// methods the plugin factories call.

// mockDBService backs the database + globals plugins with the real
// size-tracking KV layer over an in-memory kvdb mock, so the guests see genuine
// backend semantics. A fresh store per Database() call keeps each guest run
// isolated.
type mockDBService struct {
	dbIface.Service
}

func (m *mockDBService) newDatabase(name string) (dbIface.Database, error) {
	store, err := kvdbMock.New().New(logging.Logger("vm-test"), name, 0)
	if err != nil {
		return nil, err
	}
	return &mockDB{kv: dbkv.New(1<<30, name, store)}, nil
}

func (m *mockDBService) Database(dbIface.Context) (dbIface.Database, error) {
	return m.newDatabase("test")
}

func (m *mockDBService) Global(projectID string) (dbIface.Database, error) {
	return m.newDatabase("global-" + projectID)
}

type mockDB struct {
	dbIface.Database
	kv dbIface.KV
}

func (m *mockDB) KV() dbIface.KV { return m.kv }
func (m *mockDB) Close()         {}

// mockPubsubService backs the pubsub plugin with an in-memory recorder: the
// plugin's host functions call Subscribe/Publish, and the test asserts what the
// guest drove through them. No real libp2p; the guest path never leaves process.
type mockPubsubService struct {
	psIface.Service
	mu        sync.Mutex
	subs      []string           // channels the guest subscribed to
	published []pubsubPublishArg // messages the guest published
}

// pubsubMock and p2pMock are shared: TestMain hands them to the plugin, the
// tests read back what the guest drove through them.
var (
	pubsubMock = &mockPubsubService{}
	p2pMock    = &mockP2PService{}
)

type pubsubPublishArg struct {
	channel string
	data    []byte
}

func (m *mockPubsubService) Subscribe(projectId, appId, resource, channel string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs = append(m.subs, channel)
	return nil
}

func (m *mockPubsubService) Publish(_ context.Context, projectId, appId, resource, channel string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.published = append(m.published, pubsubPublishArg{channel, data})
	return nil
}

func (m *mockPubsubService) snapshot() ([]string, []pubsubPublishArg) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.subs...), append([]pubsubPublishArg(nil), m.published...)
}

// mockP2PService backs the p2p plugin's publish path (guest -> stream -> command
// -> Send). The mock command captures the body the guest sent and returns a
// canned response, which the guest writes straight back to HTTP — so the test
// asserts the full guest<->host command round-trip without a second live node.
type mockP2PService struct {
	p2pIface.Service
	mu       sync.Mutex
	lastBody []byte // last body a command was sent with
}

// p2pReply is what the mock command hands back; the guest writes it verbatim.
var p2pReply = []byte(`{"replied":"Hello from the other side"}`)

func (m *mockP2PService) Stream(_ context.Context, _, _, _ string) (p2pIface.Stream, error) {
	return &mockStream{svc: m}, nil
}

type mockStream struct{ svc *mockP2PService }

func (s *mockStream) Command(string) (p2pIface.Command, error) { return &mockCommand{svc: s.svc}, nil }
func (s *mockStream) Listen() (string, error)                  { return "/testproto/v1", nil }
func (s *mockStream) Close()                                   {}

type mockCommand struct{ svc *mockP2PService }

func (c *mockCommand) Send(_ context.Context, body map[string]interface{}) (res.Response, error) {
	c.record(body)
	return res.Response{"data": p2pReply}, nil
}

func (c *mockCommand) SendTo(_ context.Context, _ cid.Cid, body map[string]interface{}) (res.Response, error) {
	c.record(body)
	return res.Response{"data": p2pReply}, nil
}

func (c *mockCommand) record(body map[string]interface{}) {
	c.svc.mu.Lock()
	defer c.svc.mu.Unlock()
	if b, ok := body["data"].([]byte); ok {
		c.svc.lastBody = b
	}
}
