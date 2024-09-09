package config

import seer "github.com/taubyte/go-seer"

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
	NetworkType string   `yaml:"type,omitempty"`
	Network     string   `yaml:"network"`
	History     []string `yaml:"history"`
}

type Project struct {
	Name           string `yaml:"name,omitempty"`
	DefaultProfile string `yaml:"default_profile"`
	Location       string
}
