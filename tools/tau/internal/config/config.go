// Package config is the CLI's own state: which clouds a developer has logged
// in to and with what. It holds tau tokens, so it is written 0600 in a 0700
// directory and never anywhere else.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// Env names the variables that override the file — what a CI job sets instead
// of logging in.
const (
	EnvConfig = "TAU_CONFIG"
	EnvToken  = "TAU_TOKEN"
	EnvCloud  = "TAU_CLOUD"
)

// DefaultProfile is the profile a first login creates.
const DefaultProfile = "default"

// Profile is one cloud and the credential used against it.
type Profile struct {
	// Cloud is the build frontend's base URL, e.g. https://build.example.com.
	Cloud string `yaml:"cloud" json:"cloud"`
	// Token is the tau token: a short-lived access token, or an API token.
	// It is the only credential the cloud takes.
	Token string `yaml:"token,omitempty" json:"-"`
	// Expires is when Token expires, in unix seconds; zero is unknown or
	// never.
	Expires int64 `yaml:"expires,omitempty" json:"-"`
	// Refresh renews Token; empty for an API token.
	Refresh string `yaml:"refresh,omitempty" json:"-"`
	// Forges are the forge credentials a forge sign-in handed back, by forge
	// host. The CLI acts at a forge with these; the cloud holds none.
	Forges map[string]ForgeToken `yaml:"forges,omitempty" json:"-"`
}

// ForgeToken is one forge's own credential.
type ForgeToken struct {
	Token   string `yaml:"token"`
	Refresh string `yaml:"refresh,omitempty"`
	// Expires is unix seconds; zero is never.
	Expires int64  `yaml:"expires,omitempty"`
	Login   string `yaml:"login,omitempty"`
}

// Config is the whole file.
type Config struct {
	Current  string             `yaml:"current,omitempty"`
	Profiles map[string]Profile `yaml:"profiles,omitempty"`

	path string
}

// Path is where the config lives: $TAU_CONFIG, else the user config dir.
func Path() (string, error) {
	if p := os.Getenv(EnvConfig); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding the user config directory: %w", err)
	}
	return filepath.Join(dir, "tau", "config.yaml"), nil
}

// Load reads the config at path; a missing file is an empty config.
func Load(path string) (*Config, error) {
	c := &Config{path: path, Profiles: map[string]Profile{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	return c, nil
}

// Save writes the config atomically, 0600 in a 0700 directory. The temp file is
// created 0600 before any secret is written into it, so there is no moment the
// token sits in a world-readable file.
func (c *Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.path), ".config-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), c.path)
}

// File is where this config is saved.
func (c *Config) File() string { return c.path }

// Names lists the profiles, sorted.
func (c *Config) Names() []string {
	out := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Resolve is the profile a command runs against: the named one (or the current
// one), with $TAU_CLOUD/$TAU_TOKEN and then the --cloud flag laid over it.
func (c *Config) Resolve(name, cloud string) (string, Profile, error) {
	if name == "" {
		name = c.Current
	}
	if name == "" {
		name = DefaultProfile
	}
	p, known := c.Profiles[name]
	if v := os.Getenv(EnvCloud); v != "" {
		p.Cloud = v
	}
	if v := os.Getenv(EnvToken); v != "" {
		// A token from the environment is used as it is: nothing to renew it
		// with, and nowhere to save a renewal.
		p.Token, p.Expires, p.Refresh = v, 0, ""
	}
	if cloud != "" {
		p.Cloud = cloud
	}
	if !known && p.Cloud == "" {
		return name, p, fmt.Errorf("no profile %q: run `tau login --cloud <url>`", name)
	}
	if p.Cloud == "" {
		return name, p, fmt.Errorf("profile %q names no cloud: pass --cloud", name)
	}
	return name, p, nil
}
