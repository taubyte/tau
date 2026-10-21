package config

import (
	"github.com/spf13/afero"

	seer "github.com/taubyte/tau/pkg/yaseer"

	"github.com/taubyte/tau/pkg/certificate"
)

// Auth is how people sign in to the cloud (cloud.auth). The admin's password
// and the client secrets are read here and never reach a node's file.
type Auth struct {
	Admin     Admin      `yaml:"admin"`
	SignUp    string     `yaml:"signup"`
	Redirects []string   `yaml:"redirects"`
	Forges    []ForgeApp `yaml:"forges"`
	OIDC      []OIDC     `yaml:"oidc"`
}

type Admin struct {
	Name     string `yaml:"name"`
	Password string `yaml:"password"`
}

// ForgeApp is a forge the cloud serves and its OAuth app.
type ForgeApp struct {
	Host         string `yaml:"host"`
	Driver       string `yaml:"driver"`
	ClientID     string `yaml:"clientid"`
	ClientSecret string `yaml:"clientsecret"`
}

type OIDC struct {
	Name         string   `yaml:"name"`
	Issuer       string   `yaml:"issuer"`
	ClientID     string   `yaml:"clientid"`
	ClientSecret string   `yaml:"clientsecret"`
	Scopes       []string `yaml:"scopes"`
}

// CM is how the cloud issues certificates and proves custom domains
// (cloud.cm).
type CM struct {
	ACME    ACME `yaml:"acme"`
	Domains struct {
		Trusted bool `yaml:"trusted"`
	} `yaml:"domains"`
}

// ACME is the CA certificates are issued from. AccountKey is the path of the
// account's PEM key inside the config, as the CA's files are; issuance is on
// when it is set.
type ACME struct {
	Directory  string          `yaml:"directory"`
	Email      string          `yaml:"email"`
	EAB        certificate.EAB `yaml:"eab"`
	AccountKey string          `yaml:"accountkey"`
}

func (c *cloud) Auth() (a Auth, err error) {
	err = optional(c.Query.Get("auth"), &a)
	return
}

func (c *cloud) CM() (m CM, err error) {
	err = optional(c.Query.Get("cm"), &m)
	return
}

// AccountKey reads the ACME account key the config names.
func (c *cloud) AccountKey(a ACME) ([]byte, error) {
	return afero.ReadFile(c.root.fs, a.AccountKey)
}

// optional decodes q into dst, leaving dst as it was when q is not set.
func optional(q *seer.Query, dst any) error {
	var raw any
	if q.Value(&raw) != nil {
		return nil
	}
	return q.Value(dst)
}
