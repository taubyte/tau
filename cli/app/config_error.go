package app

import (
	"slices"

	"github.com/taubyte/tau/pkg/config"
	authService "github.com/taubyte/tau/services/auth"
)

// ExitConfig is EX_CONFIG. The node's systemd unit does not restart on it: a
// node its config stops would only fail again, every second, filling the journal.
const ExitConfig = 78

// ConfigError marks a failure the node's config causes, which a restart cannot fix.
type ConfigError struct{ Err error }

func (e ConfigError) Error() string { return e.Err.Error() }
func (e ConfigError) Unwrap() error { return e.Err }

// checkStartable refuses, before any service starts, a config that a service
// would refuse later: auth outside dev mode needs a tenancy.
func checkStartable(cnf config.Config) error {
	if !cnf.DevMode() && slices.Contains(cnf.Services(), "auth") && !cnf.Tenancy().Configured() {
		return authService.ErrNoTenancy
	}
	return nil
}
