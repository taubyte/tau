package app

// ExitConfig is EX_CONFIG. The node's systemd unit does not restart on it: a
// node its config stops would only fail again, every second, filling the journal.
const ExitConfig = 78

// ConfigError marks a failure the node's config causes, which a restart cannot fix.
type ConfigError struct{ Err error }

func (e ConfigError) Error() string { return e.Err.Error() }
func (e ConfigError) Unwrap() error { return e.Err }
