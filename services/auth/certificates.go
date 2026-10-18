package auth

import (
	"context"

	"connectrpc.com/connect"
	"golang.org/x/crypto/acme/autocert"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// certificateHandler implements authv2connect.CertificateServiceHandler. See
// repositories.go's repositoryHandler doc for why this is its own type
// rather than a method set on *Service — CertificateService's method names
// happen not to collide with the other four services, but every handler
// type in this package follows the same shape for consistency.
//
// THREE STORES, NEITHER MAPPING ONTO THE OTHER (auth.proto's own doc on
// Certificate): an ACME-issued certificate keyed by fqdn (GetCertificate /
// SetCertificate), an operator-injected one, also keyed by fqdn but with its
// own wildcard fallback on read (GetStaticCertificate /
// SetStaticCertificate), and autocert's own generic challenge/cache store,
// keyed by an opaque name (GetCache / SetCache / DeleteCache — the only one
// with a delete, because autocert deletes challenge tokens once an order
// completes and nothing here ever deletes an issued or injected
// certificate).
//
// GetCertificate DOES NOT FALL BACK TO GetStaticCertificate. v1's own
// getACMECertificate does (services/auth/api_acme.go), but that stitching —
// and the retry loops around both stores — lives in
// services/auth/acme/store, which is the client-side autocert.Cache
// ADAPTER composing these same primitives for a caller elsewhere in the
// cloud, and is out of scope here (auth.proto's package doc: "the
// autocert.Cache implementation ... for a caller elsewhere in the cloud").
// The two RPCs stay as separate as the two stores the proto's own comment
// describes.
type certificateHandler struct{ s *Service }

var _ authv2connect.CertificateServiceHandler = (*certificateHandler)(nil)

// GetCertificate reads the ACME-issued certificate for fqdn.
func (h *certificateHandler) GetCertificate(ctx context.Context, req *connect.Request[authv2.FqdnRequest]) (*connect.Response[authv2.Certificate], error) {
	if err := requireID("fqdn", req.Msg.GetFqdn()); err != nil {
		return nil, err
	}
	pem, err := getCertRow(ctx, h.s.kv, acmeCertRow(req.Msg.GetFqdn()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.Certificate{Pem: pem}), nil
}

// SetCertificate stores the ACME-issued certificate for fqdn.
func (h *certificateHandler) SetCertificate(ctx context.Context, req *connect.Request[authv2.SetCertificateRequest]) (*connect.Response[authv2.Empty], error) {
	if err := requireID("fqdn", req.Msg.GetFqdn()); err != nil {
		return nil, err
	}
	if err := h.s.kv.Put(ctx, acmeCertRow(req.Msg.GetFqdn()), req.Msg.GetPem()); err != nil {
		return nil, errInternal("storing certificate: %w", err)
	}
	return connect.NewResponse(&authv2.Empty{}), nil
}

// GetStaticCertificate reads the operator-injected certificate for fqdn,
// falling back to the wildcard of fqdn's parent domain on a miss — the same
// fallback services/auth/api_acme.go's getACMEStaticCertificate runs.
func (h *certificateHandler) GetStaticCertificate(ctx context.Context, req *connect.Request[authv2.FqdnRequest]) (*connect.Response[authv2.Certificate], error) {
	fqdn := req.Msg.GetFqdn()
	if err := requireID("fqdn", fqdn); err != nil {
		return nil, err
	}
	pem, err := getCertRow(ctx, h.s.kv, staticCertRow(fqdn))
	if err != nil {
		pem, err = getCertRow(ctx, h.s.kv, staticCertRow(wildcardFQDN(fqdn)))
		if err != nil {
			return nil, err
		}
	}
	return connect.NewResponse(&authv2.Certificate{Pem: pem}), nil
}

// SetStaticCertificate stores the operator-injected certificate for fqdn.
// Unlike the read side, this does not write a wildcard alias: an operator
// injecting `*.example.com` sends that literal fqdn, the same way
// services/auth/api_acme.go's setACMEStaticCertificate never rewrites what
// it was given.
func (h *certificateHandler) SetStaticCertificate(ctx context.Context, req *connect.Request[authv2.SetCertificateRequest]) (*connect.Response[authv2.Empty], error) {
	if err := requireID("fqdn", req.Msg.GetFqdn()); err != nil {
		return nil, err
	}
	if err := h.s.kv.Put(ctx, staticCertRow(req.Msg.GetFqdn()), req.Msg.GetPem()); err != nil {
		return nil, errInternal("storing static certificate: %w", err)
	}
	return connect.NewResponse(&authv2.Empty{}), nil
}

// GetCache reads an autocert cache entry by its opaque key.
func (h *certificateHandler) GetCache(ctx context.Context, req *connect.Request[authv2.CacheKeyRequest]) (*connect.Response[authv2.CacheEntry], error) {
	if err := requireID("key", req.Msg.GetKey()); err != nil {
		return nil, err
	}
	data, err := getCertRow(ctx, h.s.kv, cacheDataRow(req.Msg.GetKey()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.CacheEntry{Data: data}), nil
}

// SetCache writes an autocert cache entry. v1 also wrote a timestamp beside
// it (services/auth/api_acme.go's setACMECache) that nothing ever read back
// — CacheEntry has no timestamp field, so it is not carried forward.
func (h *certificateHandler) SetCache(ctx context.Context, req *connect.Request[authv2.SetCacheRequest]) (*connect.Response[authv2.Empty], error) {
	if err := requireID("key", req.Msg.GetKey()); err != nil {
		return nil, err
	}
	if err := h.s.kv.Put(ctx, cacheDataRow(req.Msg.GetKey()), req.Msg.GetData()); err != nil {
		return nil, errInternal("storing cache entry: %w", err)
	}
	return connect.NewResponse(&authv2.Empty{}), nil
}

// DeleteCache removes an autocert cache entry.
func (h *certificateHandler) DeleteCache(ctx context.Context, req *connect.Request[authv2.CacheKeyRequest]) (*connect.Response[authv2.Empty], error) {
	if err := requireID("key", req.Msg.GetKey()); err != nil {
		return nil, err
	}
	if err := h.s.kv.Delete(ctx, cacheDataRow(req.Msg.GetKey())); err != nil {
		return nil, errInternal("deleting cache entry: %w", err)
	}
	return connect.NewResponse(&authv2.Empty{}), nil
}

// getCertRow reads one PEM/opaque-blob row, mapping a miss — or an empty row
// left behind by a previous caller that stored nil, the same defensive case
// services/auth/api_acme.go's getACMECertificate guards — onto
// autocert.ErrCacheMiss wrapped in a NotFound, so a client that already
// knows to check for that sentinel via errors.Is still can.
func getCertRow(ctx context.Context, kv Store, row string) ([]byte, error) {
	data, err := kv.Get(ctx, row)
	if err != nil {
		return nil, errNotFound("%s: %w", row, autocert.ErrCacheMiss)
	}
	if data == nil {
		kv.Delete(ctx, row)
		return nil, errNotFound("%s: %w", row, autocert.ErrCacheMiss)
	}
	return data, nil
}
