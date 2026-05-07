package config

// Accounts holds runtime configuration for the Accounts subsystem.
//
// AccountsURL and WebAuthn are derived from NetworkFqdn at runtime
// (core/services/accounts/url.go). VerifyOnAuth is a package-level global,
// not a config field.
type Accounts struct {
	SessionTTL string        `yaml:"session-ttl,omitempty"`
	Email      AccountsEmail `yaml:"email"`
}

// AccountsEmail configures the magic-link sender. Stdout fallback auto-
// enables in DevMode; rate limits are hardcoded.
type AccountsEmail struct {
	SMTP SMTP `yaml:"smtp,omitempty"`
}

// SMTP minimal config. Empty Host = not configured (refused in production,
// stdout fallback in DevMode). Empty From defaults to `noreply@<NetworkFqdn>`.
type SMTP struct {
	Host string `yaml:"host,omitempty"`
	Port int    `yaml:"port,omitempty"`
	User string `yaml:"user,omitempty"`
	Pass string `yaml:"pass,omitempty"`
	From string `yaml:"from,omitempty"`
}
