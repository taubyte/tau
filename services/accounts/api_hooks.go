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

const (
	StreamVerbVerify                = "verify"
	StreamVerbResolve               = "resolve"
	StreamVerbLookupAccountsByEmail = "lookup_accounts_by_email"
)

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

func (srv *AccountsService) apiLookupAccountsByEmailHandler(ctx context.Context, _ streams.Connection, body command.Body) (cr.Response, error) {
	email, err := maps.String(body, "email")
	if err != nil {
		return nil, fmt.Errorf("lookup_accounts_by_email: %w", err)
	}
	ids, err := srv.Client().LookupAccountsByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	return cr.Response{"account_ids": ids}, nil
}

// apiResolveHandler is the linkage resolve: account active + git user linked →
// valid. Some builds add a separate resolve verb.
func (srv *AccountsService) apiResolveHandler(ctx context.Context, _ streams.Connection, body command.Body) (cr.Response, error) {
	accountSlug, err := maps.String(body, "account_slug")
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
	resp, err := resolveLinkage(ctx, srv.db, accountSlug, provider, externalID)
	if err != nil {
		return nil, err
	}
	return resolveResponseToWire(resp), nil
}

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
	return out
}
