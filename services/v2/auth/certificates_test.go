package auth

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/taubyte/tau/pkg/v2/proto/gen/auth/v1"
)

func TestCertificate_SetAndGet(t *testing.T) {
	s, _ := newTestService(t)
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
	s, _ := newTestService(t)
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
	s, _ := newTestService(t)
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
	s, _ := newTestService(t)
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
	s, _ := newTestService(t)
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
	s, _ := newTestService(t)
	h := &certificateHandler{s}

	_, err := h.GetStaticCertificate(t.Context(), connect.NewRequest(&authv1.FqdnRequest{Fqdn: "nowhere.example.com"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestCache_SetGetDelete(t *testing.T) {
	s, _ := newTestService(t)
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
	s, _ := newTestService(t)
	h := &certificateHandler{s}

	_, err := h.GetCache(t.Context(), connect.NewRequest(&authv1.CacheKeyRequest{Key: "never-set"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// TestCache_DeleteOfAMissingKeyIsNotAnError: autocert deletes a challenge
// token once an order completes; a second delete (or one racing another
// node) must not surface as a failure.
func TestCache_DeleteOfAMissingKeyIsNotAnError(t *testing.T) {
	s, _ := newTestService(t)
	h := &certificateHandler{s}

	_, err := h.DeleteCache(t.Context(), connect.NewRequest(&authv1.CacheKeyRequest{Key: "never-set"}))
	assert.NoError(t, err)
}

// The four tests below reach each write handler's own internal-error
// branch: h.s.kv.Put/Delete genuinely failing, not a check that was
// narrowed to let the happy path through (store_failures_test.go's
// failingKVDB wraps a real store and only the configured call fails).

func TestSetCertificate_StoreFails(t *testing.T) {
	s := &Service{kv: &failingKVDB{KVDB: newTestKV(t), failPut: true}}
	h := &certificateHandler{s}

	_, err := h.SetCertificate(t.Context(), connect.NewRequest(&authv1.SetCertificateRequest{Fqdn: "example.com", Pem: []byte("x")}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestSetStaticCertificate_StoreFails(t *testing.T) {
	s := &Service{kv: &failingKVDB{KVDB: newTestKV(t), failPut: true}}
	h := &certificateHandler{s}

	_, err := h.SetStaticCertificate(t.Context(), connect.NewRequest(&authv1.SetCertificateRequest{Fqdn: "example.com", Pem: []byte("x")}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestSetCache_StoreFails(t *testing.T) {
	s := &Service{kv: &failingKVDB{KVDB: newTestKV(t), failPut: true}}
	h := &certificateHandler{s}

	_, err := h.SetCache(t.Context(), connect.NewRequest(&authv1.SetCacheRequest{Key: "k", Data: []byte("x")}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestDeleteCache_StoreFails(t *testing.T) {
	s := &Service{kv: &failingKVDB{KVDB: newTestKV(t), failDelete: true}}
	h := &certificateHandler{s}

	_, err := h.DeleteCache(t.Context(), connect.NewRequest(&authv1.CacheKeyRequest{Key: "k"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}
