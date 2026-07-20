package accounts

import (
	"context"
	"errors"

	"github.com/taubyte/tau/core/kvdb"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
)

// resolveLinkage is the community access check: Valid iff the account exists,
// is active, and the git user is linked to it. Shared by the in-process
// Validate (community build) and the wire handler.
func resolveLinkage(ctx context.Context, db kvdb.KVDB, accountSlug, provider, externalID string) (*accountsIface.ResolveResponse, error) {
	acc, err := newAccountStore(db).GetBySlug(ctx, accountSlug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return &accountsIface.ResolveResponse{Valid: false, Reason: "account not found"}, nil
		}
		return nil, err
	}
	if acc.Status != accountsIface.AccountStatusActive {
		return &accountsIface.ResolveResponse{Valid: false, Reason: "account not active"}, nil
	}
	if _, err := newUserStore(db, acc.ID).GetByExternal(ctx, provider, externalID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return &accountsIface.ResolveResponse{Valid: false, Reason: "git user not linked to account"}, nil
		}
		return nil, err
	}
	return &accountsIface.ResolveResponse{Valid: true}, nil
}
