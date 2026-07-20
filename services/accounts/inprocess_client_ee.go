//go:build ee

package accounts

import (
	"context"
	"errors"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	eestore "github.com/taubyte/tau/ee/services/accounts"
)

// eeSurface is defined in the ee package and only aliased here; every method it
// carries (Validate + the rest) is injected at construction over the accounts
// KV — none is spelled or called in this tree.
type eeSurface = eestore.Surface

func newInProcessClient(srv *AccountsService) accountsIface.Client {
	c := newBase(srv)
	c.eeSurface = eestore.NewSurface(srv.db,
		func(ctx context.Context, accountID, userID string) error {
			_, err := newUserStore(srv.db, accountID).Get(ctx, userID)
			return err
		},
		func(ctx context.Context, slug string) (string, bool, bool, error) {
			acc, err := c.accounts.GetBySlug(ctx, slug)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					return "", false, false, nil
				}
				return "", false, false, err
			}
			return acc.ID, acc.Status == accountsIface.AccountStatusActive, true, nil
		},
		func(ctx context.Context, accountID, provider, externalID string) (string, bool, error) {
			u, err := newUserStore(srv.db, accountID).GetByExternal(ctx, provider, externalID)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					return "", false, nil
				}
				return "", false, err
			}
			return u.ID, true, nil
		},
	)
	return c
}
