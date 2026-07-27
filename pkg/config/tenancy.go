package config

import "strings"

// Tenancy names the git namespace (GitHub org, GitLab group, Bitbucket
// workspace) that owns this cloud. Owner is the owning segment of `owner/repo`,
// which is what a repository URI yields on every provider.
//
// An empty Owner means no namespace is configured. Registration refuses in that
// state rather than falling open, so a cloud is never accidentally open to
// anyone holding a valid provider token.
type Tenancy struct {
	Provider string     `yaml:"provider,omitempty"`
	Owner    string     `yaml:"owner,omitempty"`
	App      TenancyApp `yaml:"app,omitempty"`
}

// TenancyApp is the credential the namespace itself granted. The caller's own
// token cannot answer questions about other users or private repositories, so
// membership is checked with an installation token minted from this key.
//
// Key is the PEM inline, matching how Privatekey and Swarmkey already travel.
type TenancyApp struct {
	ClientId string `yaml:"client-id,omitempty"`
	Key      string `yaml:"key,omitempty"`
}

// Configured reports whether an owning namespace was set.
func (t Tenancy) Configured() bool { return t.Owner != "" }

// Owns reports whether fullName ("owner/repo") belongs to the configured
// namespace. Provider names are case-insensitive, so the comparison is too.
func (t Tenancy) Owns(fullName string) bool {
	owner, _, ok := strings.Cut(fullName, "/")
	if !ok || owner == "" {
		return false
	}
	return strings.EqualFold(owner, t.Owner)
}
