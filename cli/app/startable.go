//go:build !ee

package app

import (
	"slices"

	"github.com/taubyte/tau/pkg/config"
	authService "github.com/taubyte/tau/services/auth"
)

// checkStartable refuses, before any service starts, a config that a service
// would refuse later: auth outside dev mode needs a tenancy.
func checkStartable(cnf config.Config) error {
	if !cnf.DevMode() && slices.Contains(cnf.Services(), "auth") && !cnf.Tenancy().Configured() {
		return authService.ErrNoTenancy
	}
	return nil
}
