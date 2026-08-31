package auth

import (
	"testing"

	logpkg "github.com/ipfs/go-log/v2"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	taupeer "github.com/taubyte/tau/p2p/peer"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
)

// testLogger satisfies the log.StandardLogger factory.New wants, without
// pulling every test's output through go-log's global config.
var testLogger = logpkg.Logger("tau.v2.auth.test")

// testConfig is the Config double every test in this package builds a
// Service from: four fields standing in for pkg/config's forty-six methods
// (service-conventions.md §2b), which is the whole reason a Service can be
// constructed and inspected without a port, a listener or dream.
type testConfig struct {
	node    taupeer.Node
	dbs     kvdb.Factory
	devMode bool
	dvPriv  []byte
	dvPub   []byte
}

func (c *testConfig) Root() string                         { return "" }
func (c *testConfig) DevMode() bool                        { return c.devMode }
func (c *testConfig) Node() taupeer.Node                   { return c.node }
func (c *testConfig) Databases() kvdb.Factory              { return c.dbs }
func (c *testConfig) DomainValidation() (priv, pub []byte) { return c.dvPriv, c.dvPub }

var _ Config = (*testConfig)(nil)

// newMockNode returns a mocknet-backed peer.Node, cleaned up at test end.
// p2p/rpc's own measurement (service-conventions.md §0.1) is that connect-go
// over gostream over this exact mocknet round-trips in well under a second
// with no TCP port bound — confirmed again for this package's own dependency
// version before any test below was written on top of it — which is what
// makes every RPC-level test in this package (rpc_test.go, authz_test.go)
// affordable to run through the real transport rather than faking the one
// thing (rpc.RemotePeer) that must never be fakeable.
func newMockNode(t testing.TB) taupeer.Node {
	t.Helper()
	n := taupeer.Mock(t.Context())
	t.Cleanup(n.Close)
	return n
}

// linkMockPeers links every mock node created so far (taupeer.Mock shares
// one process-wide mocknet, the same pattern pkg/raft/healing_test.go uses)
// so they can dial each other.
func linkMockPeers(t testing.TB) {
	t.Helper()
	require.NoError(t, taupeer.LinkAllPeers())
}

// testDomainValidationKeys returns a fresh, valid P-256 PEM key pair in the
// exact shape github.com/taubyte/domain-validation's PrivateKey/PublicKey
// options parse (store.go's generateECKeyPair doc explains the shape).
func testDomainValidationKeys(t testing.TB) (priv, pub []byte) {
	t.Helper()
	privatePEM, publicPEM, err := generateECKeyPair()
	require.NoError(t, err)
	return []byte(privatePEM), []byte(publicPEM)
}

// newTestConfig builds a Config around node with a fresh mock kvdb.Factory
// and a valid domain-validation key pair — everything New requires and
// nothing more.
func newTestConfig(t testing.TB, node taupeer.Node) *testConfig {
	t.Helper()
	priv, pub := testDomainValidationKeys(t)
	return &testConfig{
		node:   node,
		dbs:    kvdbmock.New(),
		dvPriv: priv,
		dvPub:  pub,
	}
}

// newTestService constructs (but does not Start) a Service on a fresh mock
// node, cleaned up at test end. Handler-level tests call a handler's methods
// directly — service-conventions.md §6a's "no wire, no server, no
// listener" — so construction alone is all most of this package's tests
// need; rpc_test.go and authz_test.go Start it themselves where a live
// listener and a second peer are the point.
func newTestService(t testing.TB, opts ...Option) (*Service, taupeer.Node) {
	t.Helper()
	node := newMockNode(t)
	s, err := New(t.Context(), newTestConfig(t, node), opts...)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s, node
}
