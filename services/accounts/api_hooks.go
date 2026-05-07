package accounts

import (
	"context"
	"fmt"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/tau/utils/maps"
)

// Verify / Resolve P2P stream verb names.
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

// --- wire encoders -------------------------------------------------
//
// Nested structured fields ride the wire natively — the P2P framer CBORs
// the whole response, the HTTP wrapper JSONs it. The client side has its
// own decoders in clients/p2p/accounts/client.go.

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
