package auth

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/auth/v1"
)

func TestCertificate_SetAndGet(t *testing.T) {
	s := newTestService(t)
	h := &certificateHandler{s}

	_, err := h.SetCertificate(t.Context(), connect.NewRequest(&authv1.SetCertificateRequest{
		Fqdn: "example.com", Pem: []byte("acme-pem"),
	}))
	require.NoError(t, err)

	res, err := h.GetCertificate(t.Context(), connect.NewRequest(&authv1.FqdnRequest{Fqdn: "example.com"}))
	require.NoError(t, err)
	assert.Equal(t, []byte("acme-pem"), res.Msg.GetPem())
}

func TestCertificate_GetMiss(t *testing.T) {
	s := newTestService(t)
	h := &certificateHandler{s}

	_, err := h.GetCertificate(t.Context(), connect.NewRequest(&authv1.FqdnRequest{Fqdn: "missing.example.com"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// TestCertificate_GetDoesNotFallBackToStatic pins auth.proto's own doc:
// GetCertificate and GetStaticCertificate read two stores that neither maps
// onto the other. v1's getACMECertificate falls back to the static store on
// a miss (services/auth/api_acme.go); that stitching belongs to the
// client-side autocert.Cache adapter this schema does not model
// (certificates.go's package doc), so a static-only certificate must not be
// visible through GetCertificate.
func TestCertificate_GetDoesNotFallBackToStatic(t *testing.T) {
	s := newTestService(t)
	h := &certificateHandler{s}

	_, err := h.SetStaticCertificate(t.Context(), connect.NewRequest(&authv1.SetCertificateRequest{
		Fqdn: "example.com", Pem: []byte("static-pem"),
	}))
	require.NoError(t, err)

	_, err = h.GetCertificate(t.Context(), connect.NewRequest(&authv1.FqdnRequest{Fqdn: "example.com"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestStaticCertificate_SetAndGet(t *testing.T) {
	s := newTestService(t)
	h := &certificateHandler{s}

	_, err := h.SetStaticCertificate(t.Context(), connect.NewRequest(&authv1.SetCertificateRequest{
		Fqdn: "www.example.com", Pem: []byte("static-pem"),
	}))
	require.NoError(t, err)

	res, err := h.GetStaticCertificate(t.Context(), connect.NewRequest(&authv1.FqdnRequest{Fqdn: "www.example.com"}))
	require.NoError(t, err)
	assert.Equal(t, []byte("static-pem"), res.Msg.GetPem())
}

// TestStaticCertificate_WildcardFallback: a request for a name with no
// certificate of its own falls back to its parent domain's wildcard, the
// same fallback services/auth/api_acme.go's getACMEStaticCertificate runs.
func TestStaticCertificate_WildcardFallback(t *testing.T) {
	s := newTestService(t)
	h := &certificateHandler{s}

	_, err := h.SetStaticCertificate(t.Context(), connect.NewRequest(&authv1.SetCertificateRequest{
		Fqdn: "*.example.com", Pem: []byte("wildcard-pem"),
	}))
	require.NoError(t, err)

	res, err := h.GetStaticCertificate(t.Context(), connect.NewRequest(&authv1.FqdnRequest{Fqdn: "anything.example.com"}))
	require.NoError(t, err)
	assert.Equal(t, []byte("wildcard-pem"), res.Msg.GetPem())
}

func TestStaticCertificate_Miss(t *testing.T) {
	s := newTestService(t)
	h := &certificateHandler{s}

	_, err := h.GetStaticCertificate(t.Context(), connect.NewRequest(&authv1.FqdnRequest{Fqdn: "nowhere.example.com"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestCache_SetGetDelete(t *testing.T) {
	s := newTestService(t)
	h := &certificateHandler{s}

	_, err := h.SetCache(t.Context(), connect.NewRequest(&authv1.SetCacheRequest{
		Key: "challenge-token", Data: []byte("challenge-data"),
	}))
	require.NoError(t, err)

	res, err := h.GetCache(t.Context(), connect.NewRequest(&authv1.CacheKeyRequest{Key: "challenge-token"}))
	require.NoError(t, err)
	assert.Equal(t, []byte("challenge-data"), res.Msg.GetData())

	_, err = h.DeleteCache(t.Context(), connect.NewRequest(&authv1.CacheKeyRequest{Key: "challenge-token"}))
	require.NoError(t, err)

	_, err = h.GetCache(t.Context(), connect.NewRequest(&authv1.CacheKeyRequest{Key: "challenge-token"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestCache_GetMiss(t *testing.T) {
	s := newTestService(t)
	h := &certificateHandler{s}

	_, err := h.GetCache(t.Context(), connect.NewRequest(&authv1.CacheKeyRequest{Key: "never-set"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// TestCache_DeleteOfAMissingKeyIsNotAnError: autocert deletes a challenge
// token once an order completes; a second delete (or one racing another
// node) must not surface as a failure.
func TestCache_DeleteOfAMissingKeyIsNotAnError(t *testing.T) {
	s := newTestService(t)
	h := &certificateHandler{s}

	_, err := h.DeleteCache(t.Context(), connect.NewRequest(&authv1.CacheKeyRequest{Key: "never-set"}))
	assert.NoError(t, err)
}

// The four tests below reach each write handler's own internal-error
// branch: h.s.kv.Put/Delete genuinely failing, not a check that was
// narrowed to let the happy path through (store_failures_test.go's
// failingKVDB wraps a real store and only the configured call fails).

func TestSetCertificate_StoreFails(t *testing.T) {
	s := &Service{kv: &failingKVDB{Store: newStore(t), failPut: true}}
	h := &certificateHandler{s}

	_, err := h.SetCertificate(t.Context(), connect.NewRequest(&authv1.SetCertificateRequest{Fqdn: "example.com", Pem: []byte("x")}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestSetStaticCertificate_StoreFails(t *testing.T) {
	s := &Service{kv: &failingKVDB{Store: newStore(t), failPut: true}}
	h := &certificateHandler{s}

	_, err := h.SetStaticCertificate(t.Context(), connect.NewRequest(&authv1.SetCertificateRequest{Fqdn: "example.com", Pem: []byte("x")}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestSetCache_StoreFails(t *testing.T) {
	s := &Service{kv: &failingKVDB{Store: newStore(t), failPut: true}}
	h := &certificateHandler{s}

	_, err := h.SetCache(t.Context(), connect.NewRequest(&authv1.SetCacheRequest{Key: "k", Data: []byte("x")}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestDeleteCache_StoreFails(t *testing.T) {
	s := &Service{kv: &failingKVDB{Store: newStore(t), failDelete: true}}
	h := &certificateHandler{s}

	_, err := h.DeleteCache(t.Context(), connect.NewRequest(&authv1.CacheKeyRequest{Key: "k"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

// TestEveryWireIdIsRefusedEmpty covers store.go's one grammar precondition
// at every edge that can break it: an id that reaches a row builder empty
// encodes to an empty segment, which go-datastore then cleans away
// entirely, moving a boundary. Each RPC below turns a caller-supplied string
// straight into a key, so each one has to refuse the empty string itself —
// the repository RPCs are absent from the list because repokey.Parse
// already refuses it for them (repositories_test.go).
func TestEveryWireIdIsRefusedEmpty(t *testing.T) {
	store := newStore(t)
	s, err := New(newHost(t, newMocknet(t)), store, newTestConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	certs := &certificateHandler{s}

	for name, call := range map[string]func() error{
		"CertificateService.GetCertificate": func() error {
			_, err := certs.GetCertificate(t.Context(), connect.NewRequest(&authv1.FqdnRequest{}))
			return err
		},
		"CertificateService.SetCertificate": func() error {
			_, err := certs.SetCertificate(t.Context(), connect.NewRequest(&authv1.SetCertificateRequest{Pem: []byte("pem")}))
			return err
		},
		"CertificateService.GetStaticCertificate": func() error {
			_, err := certs.GetStaticCertificate(t.Context(), connect.NewRequest(&authv1.FqdnRequest{}))
			return err
		},
		"CertificateService.SetStaticCertificate": func() error {
			_, err := certs.SetStaticCertificate(t.Context(), connect.NewRequest(&authv1.SetCertificateRequest{Pem: []byte("pem")}))
			return err
		},
		"CertificateService.GetCache": func() error {
			_, err := certs.GetCache(t.Context(), connect.NewRequest(&authv1.CacheKeyRequest{}))
			return err
		},
		"CertificateService.SetCache": func() error {
			_, err := certs.SetCache(t.Context(), connect.NewRequest(&authv1.SetCacheRequest{Data: []byte("d")}))
			return err
		},
		"CertificateService.DeleteCache": func() error {
			_, err := certs.DeleteCache(t.Context(), connect.NewRequest(&authv1.CacheKeyRequest{}))
			return err
		},
		"HookService.Get": func() error {
			_, err := (&hookHandler{s}).Get(t.Context(), connect.NewRequest(&authv1.HookRef{}))
			return err
		},
		"ProjectService.Get": func() error {
			_, err := (&projectHandler{s}).Get(t.Context(), connect.NewRequest(&authv1.ProjectRef{}))
			return err
		},
		"DomainService.Register": func() error {
			_, err := (&domainHandler{s}).Register(t.Context(), connect.NewRequest(&authv1.RegisterDomainRequest{ProjectId: testProjectID(t)}))
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			require.Error(t, err)
			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}

	// And nothing reached the store: an empty id must leave no row behind,
	// least of all one a later listing has to reason about.
	rows, err := store.List(t.Context(), "/")
	require.NoError(t, err)
	assert.Empty(t, rows)
}
