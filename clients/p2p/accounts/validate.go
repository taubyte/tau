//go:build !ee

package accounts

import (
	"context"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	project "github.com/taubyte/tau/pkg/schema/project"
)

// Validate (community build) checks linkage only against the account the
// binding names.
func (c *Client) Validate(ctx context.Context, provider, externalID string, binding project.CloudBinding) (*accountsIface.ResolveResponse, error) {
	return c.sendLinkageResolve(binding.Account, provider, externalID)
}
