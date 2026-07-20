//go:build ee

package fixtures

import (
	"context"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	eefixtures "github.com/taubyte/tau/ee/dream/fixtures"
)

func seedAccountExtras(ctx context.Context, cli accountsIface.Client, accountID, userID string) error {
	return eefixtures.Seed(ctx, cli, accountID, userID)
}
