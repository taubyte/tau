package tests

import (
	"bytes"
	"context"
	"io"
	"sort"
	"strconv"
	"sync"

	cid "github.com/ipfs/go-cid"
	logging "github.com/ipfs/go-log/v2"
	mh "github.com/multiformats/go-multihash"
	dbIface "github.com/taubyte/tau/core/services/substrate/components/database"
	p2pIface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	psIface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
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

// mockStorageService backs the storage plugin with an in-memory versioned store.
// The plugin caches the Storage it gets per guest, so a fresh store per call
// isolates each run. CIDs are the real content hash (so they round-trip through
// WriteCid/ReadCid) but nothing is content-addressed off-box — the point is the
// host ABI, not IPFS.
type mockStorageService struct {
	storageIface.Service
}

func (m *mockStorageService) Storage(storageIface.Context) (storageIface.Storage, error) {
	return newMockStorage(), nil
}

func (m *mockStorageService) Get(storageIface.Context) (storageIface.Storage, error) {
	return newMockStorage(), nil
}

type mockStorage struct {
	storageIface.Storage
	mu    sync.Mutex
	files map[string]map[int][]byte // name -> version -> bytes
	next  map[string]int            // name -> next version to assign
	cap   int
}

func newMockStorage() *mockStorage {
	return &mockStorage{files: map[string]map[int][]byte{}, next: map[string]int{}, cap: 1 << 20}
}

func (s *mockStorage) latest(name string) int {
	max := 0
	for v := range s.files[name] {
		if v > max {
			max = v
		}
	}
	return max
}

func (s *mockStorage) AddFile(_ context.Context, r io.ReadSeeker, name string, replace bool) (int, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.files[name] == nil {
		s.files[name] = map[int][]byte{}
		s.next[name] = 1
	}
	version := s.next[name]
	if replace && s.latest(name) > 0 {
		version = s.latest(name) // overwrite current version in place
	} else {
		s.next[name] = version + 1
	}
	s.files[name][version] = data
	return version, nil
}

func (s *mockStorage) Meta(_ context.Context, name string, version int) (storageIface.Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if version <= 0 {
		version = s.latest(name)
	}
	data, ok := s.files[name][version]
	if !ok {
		return nil, io.EOF
	}
	return &mockMeta{data: data, version: version}, nil
}

func (s *mockStorage) ListVersions(_ context.Context, name string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.files[name]) == 0 {
		return nil, io.EOF
	}
	out := make([]string, 0, len(s.files[name]))
	for v := range s.files[name] {
		out = append(out, strconv.Itoa(v))
	}
	sort.Strings(out)
	return out, nil
}

func (s *mockStorage) DeleteFile(_ context.Context, name string, version int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files[name], version)
	if len(s.files[name]) == 0 {
		delete(s.files, name)
	}
	return nil
}

func (s *mockStorage) GetLatestVersion(_ context.Context, name string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest(name), nil
}

// List returns keys shaped "/file/<name>/<version>" (one per version), matching
// the real store's layout. The leading slash matters: the codec joins entries
// with \x00 and the go-sdk's ListFiles splits the whole buffer on "/", so the
// slash after each separator keeps every "file" token parseable.
func (s *mockStorage) List(_ context.Context, _ string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0)
	for name, versions := range s.files {
		for v := range versions {
			out = append(out, "/file/"+name+"/"+strconv.Itoa(v))
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *mockStorage) Used(context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	used := 0
	for _, versions := range s.files {
		for _, b := range versions {
			used += len(b)
		}
	}
	return used, nil
}

func (s *mockStorage) Capacity() int { return s.cap }
func (s *mockStorage) Close()        {}

type mockMeta struct {
	data    []byte
	version int
}

func (m *mockMeta) Get() (io.ReadSeekCloser, error) {
	return nopSeekCloser{bytes.NewReader(m.data)}, nil
}
func (m *mockMeta) Version() int { return m.version }

func (m *mockMeta) Cid() cid.Cid {
	sum, err := mh.Sum(m.data, mh.SHA2_256, -1)
	if err != nil {
		return cid.Undef
	}
	return cid.NewCidV1(cid.Raw, sum)
}

type nopSeekCloser struct{ io.ReadSeeker }

func (nopSeekCloser) Close() error { return nil }
