package accounts

import (
	"context"
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/tau/utils/maps"
)

// P2P stream verb names exposed by services/auth and the project compiler.
const (
	StreamVerbVerify  = "verify"
	StreamVerbResolve = "resolve"
)

// apiVerifyHandler — body: {provider, external_id}; response: VerifyResponse.
func (srv *AccountsService) apiVerifyHandler(ctx context.Context, _ streams.Connection, body command.Body) (cr.Response, error) {
	provider, err := maps.String(body, "provider")
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	externalID, err := maps.String(body, "external_id")
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	resp, err := srv.Client().Verify(ctx, provider, externalID)
	if err != nil {
		return nil, err
	}
	return verifyResponseToWire(resp), nil
}

// apiResolveHandler — body: {account_slug, plan_slug, provider, external_id};
// response: ResolveResponse.
func (srv *AccountsService) apiResolveHandler(ctx context.Context, _ streams.Connection, body command.Body) (cr.Response, error) {
	accountSlug, err := maps.String(body, "account_slug")
	if err != nil {
		return nil, fmt.Errorf("resolve: %w", err)
	}
	planSlug, err := maps.String(body, "plan_slug")
	if err != nil {
		return nil, fmt.Errorf("resolve: %w", err)
	}
	provider, err := maps.String(body, "provider")
	if err != nil {
		return nil, fmt.Errorf("resolve: %w", err)
	}
	externalID, err := maps.String(body, "external_id")
	if err != nil {
		return nil, fmt.Errorf("resolve: %w", err)
	}
	resp, err := srv.Client().ResolvePlan(ctx, accountSlug, planSlug, provider, externalID)
	if err != nil {
		return nil, err
	}
	return resolveResponseToWire(resp), nil
}

// --- wire codec ---------------------------------------------------
//
// Nested structured fields ride the wire natively — the P2P framer CBORs
// the whole response, the HTTP wrapper JSONs it. Receiver round-trips
// through CBOR (via decodeResponseField) to land in a typed struct, same
// approach used by api_management.go for management verbs.

func verifyResponseToWire(r *accountsIface.VerifyResponse) cr.Response {
	if r == nil {
		return cr.Response{"linked": false}
	}
	out := cr.Response{"linked": r.Linked}
	if len(r.Accounts) > 0 {
		out["accounts"] = r.Accounts
	}
	return out
}

func resolveResponseToWire(r *accountsIface.ResolveResponse) cr.Response {
	if r == nil {
		return cr.Response{"valid": false, "reason": "nil response"}
	}
	out := cr.Response{"valid": r.Valid}
	if r.Reason != "" {
		out["reason"] = r.Reason
	}
	if r.Plan != nil {
		out["plan"] = r.Plan
	}
	return out
}

func tryBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// decodeResponseField round-trips m[key] through CBOR into out. Used by the
// FromWire decoders below since map[string]any → typed-struct needs a
// re-encode pass.
func decodeResponseField(m map[string]any, key string, out any) error {
	v, ok := m[key]
	if !ok {
		return errors.New("missing " + key)
	}
	raw, err := cbor.Marshal(v)
	if err != nil {
		return fmt.Errorf("re-encode %s: %w", key, err)
	}
	if err := cbor.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s: %w", key, err)
	}
	return nil
}

// VerifyResponseFromWire reconstructs a VerifyResponse from a wire response.
func VerifyResponseFromWire(resp map[string]interface{}) (*accountsIface.VerifyResponse, error) {
	out := &accountsIface.VerifyResponse{
		Linked: tryBool(resp, "linked"),
	}
	if _, ok := resp["accounts"]; ok {
		if err := decodeResponseField(resp, "accounts", &out.Accounts); err != nil {
			return nil, fmt.Errorf("verify: %w", err)
		}
	}
	return out, nil
}

// ResolveResponseFromWire reconstructs a ResolveResponse from a wire response.
func ResolveResponseFromWire(resp map[string]interface{}) (*accountsIface.ResolveResponse, error) {
	out := &accountsIface.ResolveResponse{
		Valid:  tryBool(resp, "valid"),
		Reason: maps.TryString(resp, "reason"),
	}
	if _, ok := resp["plan"]; ok {
		var b accountsIface.Plan
		if err := decodeResponseField(resp, "plan", &b); err != nil {
			return nil, fmt.Errorf("resolve: %w", err)
		}
		out.Plan = &b
	}
	return out, nil
}
