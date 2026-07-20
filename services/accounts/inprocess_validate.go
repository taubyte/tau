//go:build !ee

package accounts

import (
	"context"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	project "github.com/taubyte/tau/pkg/schema/project"
)

// Validate (community build) checks linkage only: the account named by the
// binding must be active and the git user linked to it. Everything else the
// binding carries is advisory here.
func (c *inProcessClient) Validate(ctx context.Context, provider, externalID string, binding project.CloudBinding) (*accountsIface.ResolveResponse, error) {
	return resolveLinkage(ctx, c.srv.db, binding.Account, provider, externalID)
}
