package config

import seer "github.com/taubyte/tau/pkg/yaseer"

type tauConfig struct {
	root *seer.Seer
}

type profileHandler struct{}

type projectHandler struct{}

type Profile struct {
	// name is not exported to yaml because it's the key
	name string

	Provider string
	Token    string
	Default  bool

	// TODO get from config when verifying token
	// may need to fake in tests
	GitUsername string   `yaml:"git_username"`
	GitEmail    string   `yaml:"git_email"`
	CloudType   string   `yaml:"type,omitempty"`
	Cloud       string   `yaml:"network"`
	History     []string `yaml:"history"`

	// AccountsSession is the Member-session bearer for the tau accounts
	// service, persisted by `tau accounts login` and consumed by `tau
	// accounts whoami` / `logout`. Kept on the same Profile so a single
	// "logged-in identity" carries both git-side OAuth (Token above) and
	// Account-side session in one place.
	AccountsSession string `yaml:"accounts_session,omitempty"`
}

type Project struct {
	Name           string `yaml:"name,omitempty"`
	DefaultProfile string `yaml:"default_profile"`
	Location       string
}
