package accounts

import (
	"context"
	"crypto/rand"
	"fmt"

	"github.com/taubyte/tau/core/kvdb"
)

// signingKeyPath returns the KV path for one Account's HMAC signing key.
func signingKeyPath(accountID string) string {
	return prefixAccounts + accountID + "/signing_key"
}

// loadOrCreateAccountSigningKey returns the per-Account HMAC signing key,
// generating it on first use. 32 bytes of cryptographic randomness.
//
// Used to sign Member session tokens (HMAC over `{member_id, account_id,
// exp}`). A future PR can move this storage into the EE secrets store
// behind a build-tag seam without changing the call sites here.
func loadOrCreateAccountSigningKey(ctx context.Context, db kvdb.KVDB, accountID string) ([]byte, error) {
	key, err := db.Get(ctx, signingKeyPath(accountID))
	if err == nil && len(key) > 0 {
		return key, nil
	}
	if err != nil && !isMissing(err) {
		return nil, fmt.Errorf("accounts: read signing key: %w", err)
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("accounts: generate signing key: %w", err)
	}
	if err := db.Put(ctx, signingKeyPath(accountID), key); err != nil {
		return nil, fmt.Errorf("accounts: persist signing key: %w", err)
	}
	return key, nil
}
