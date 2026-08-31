package auth

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/ipfs/go-log/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/rpc"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
)

// erroringFactory always fails New, so a store-open failure in New (Service)
// has a test that reaches it (service-conventions.md §6c(1): "Every error
// return of New has a test that reaches it").
type erroringFactory struct{}

func (erroringFactory) New(log.StandardLogger, string, int) (kvdb.KVDB, error) {
	return nil, errors.New("store unavailable")
}
func (erroringFactory) Close() {}

func TestNew_NoNode(t *testing.T) {
	cfg := newTestConfig(t, nil)
	_, err := New(t.Context(), cfg)
	assert.Error(t, err)
}

func TestNew_EmptyDomainValidationKeys(t *testing.T) {
	node := newMockNode(t)
	for _, tc := range []struct {
		name      string
		priv, pub []byte
	}{
		{"both empty", nil, nil},
		{"private empty", nil, []byte("pub")},
		{"public empty", []byte("priv"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &testConfig{node: node, dbs: kvdbmock.New(), dvPriv: tc.priv, dvPub: tc.pub}
			_, err := New(t.Context(), cfg)
			assert.Error(t, err)
		})
	}
}

func TestNew_NoDatabaseFactory(t *testing.T) {
	node := newMockNode(t)
	priv, pub := testDomainValidationKeys(t)
	cfg := &testConfig{node: node, dbs: nil, dvPriv: priv, dvPub: pub}
	_, err := New(t.Context(), cfg)
	assert.Error(t, err)
}

func TestNew_StoreOpenFails(t *testing.T) {
	node := newMockNode(t)
	priv, pub := testDomainValidationKeys(t)
	cfg := &testConfig{node: node, dbs: erroringFactory{}, dvPriv: priv, dvPub: pub}
	_, err := New(t.Context(), cfg)
	assert.Error(t, err)
}

func TestNew_OptionFailureClosesTheStoreItAlreadyOpened(t *testing.T) {
	node := newMockNode(t)
	closed := false
	failOpt := Option(func(*Service) error {
		return errors.New("deliberate option failure")
	})

	factory := &closeTrackingFactory{Factory: kvdbmock.New(), closed: &closed}
	cfg := newTestConfig(t, node)
	cfg.dbs = factory

	_, err := New(t.Context(), cfg, failOpt)
	require.Error(t, err)
	assert.True(t, closed, "New must close the store it opened before an Option failed")
}

// closeTrackingFactory wraps a real kvdb.Factory and records whether the one
// database it opened was closed.
type closeTrackingFactory struct {
	kvdb.Factory
	closed *bool
}

func (f *closeTrackingFactory) New(logger log.StandardLogger, path string, rebroadcast int) (kvdb.KVDB, error) {
	db, err := f.Factory.New(logger, path, rebroadcast)
	if err != nil {
		return nil, err
	}
	return &closeTrackingKVDB{KVDB: db, closed: f.closed}, nil
}

type closeTrackingKVDB struct {
	kvdb.KVDB
	closed *bool
}

func (k *closeTrackingKVDB) Close() {
	*k.closed = true
	k.KVDB.Close()
}

func TestNew_DevModeIsAcceptedAndDoesNotBindAnything(t *testing.T) {
	node := newMockNode(t)
	cfg := newTestConfig(t, node)
	cfg.devMode = true
	s, err := New(t.Context(), cfg)
	require.NoError(t, err)
	defer s.Close()
	// Constructing must not have started a listener (service-conventions.md
	// §2a): nothing here to assert on directly since Service does not
	// expose its listener state, but a second Service on the SAME node and
	// protocol succeeding to construct too is the observable half of "New
	// binds nothing" — Start is what would collide.
	_, err = New(t.Context(), newTestConfig(t, node))
	assert.NoError(t, err)
}

func TestStartAndClose(t *testing.T) {
	s, _ := newTestService(t)

	require.NoError(t, s.Start(t.Context()))

	// Starting twice is an error, not a second listener.
	assert.Error(t, s.Start(t.Context()))

	require.NoError(t, s.Close())
	// Close is idempotent.
	assert.NoError(t, s.Close())

	// Start after Close is an error, not a listener nobody will ever stop.
	assert.Error(t, s.Start(t.Context()))
}

func TestClose_WithoutStart(t *testing.T) {
	s, _ := newTestService(t)
	assert.NoError(t, s.Close())
	assert.NoError(t, s.Close())
}

func TestClose_NeverClosesTheSharedFactory(t *testing.T) {
	node := newMockNode(t)
	factory := kvdbmock.New()
	cfg := newTestConfig(t, node)
	cfg.dbs = factory

	s, err := New(t.Context(), cfg)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	// The factory is still usable after this service closed its own
	// database from it — Close must never call factory.Close(), which would
	// tear down every other service's database sharing the same factory
	// (service-conventions.md §2e).
	db, err := factory.New(testLogger, "some-other-service", 5)
	require.NoError(t, err)
	require.NoError(t, db.Put(t.Context(), "/probe", []byte("x")))
}

func TestWithServerOptions(t *testing.T) {
	node := newMockNode(t)
	s, err := New(t.Context(), newTestConfig(t, node), WithServerOptions(rpc.WithPrefaceTimeout(time.Second)))
	require.NoError(t, err)
	defer s.Close()

	assert.Len(t, s.serverOptions, 1, "the ServerOption must reach the Service, for Start to pass to rpc.Listen")
	require.NoError(t, s.Start(t.Context()))
}

// TestStart_ListenFails reaches Start's own error return: p2p/rpc.Listen
// refuses a ServerOption that sets a field it owns (WithHTTPServer setting
// Handler — p2p/rpc/server.go's refuseOwnedFields), which is the cheapest
// way to make the real rpc.Listen call fail without needing a second
// listener already bound on the same protocol.
func TestStart_ListenFails(t *testing.T) {
	s, _ := newTestService(t, WithServerOptions(rpc.WithHTTPServer(func(hs *http.Server) {
		hs.Handler = http.NewServeMux()
	})))
	assert.Error(t, s.Start(t.Context()))
}

func TestDescriptor(t *testing.T) {
	assert.Equal(t, "auth", Descriptor.Name)
	assert.Equal(t, IngressNone, Descriptor.Ingress)
	assert.Equal(t, Protocol, Descriptor.Protocol)
}
