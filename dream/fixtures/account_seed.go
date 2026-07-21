//go:build !ee

package fixtures

import (
	"context"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
)

// seedAccountExtras is a no-op in the community build — a linked user is the
// whole access grant, so there is nothing extra to seed. The ee build
// seeds its fixture data (see account_seed_ee.go).
func seedAccountExtras(_ context.Context, _ accountsIface.Client, _, _ string) error {
	return nil
}
