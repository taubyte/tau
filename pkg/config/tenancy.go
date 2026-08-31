package config

import (
	"strings"

	"github.com/taubyte/tau/pkg/git/repokey"
)

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
// namespace. This is the shape a provider API hands back, where which provider
// answered is already decided by the client that asked.
func (t Tenancy) Owns(fullName string) bool {
	owner, _, ok := strings.Cut(fullName, "/")
	if !ok {
		return false
	}
	return t.owns(owner)
}

// OwnsCloneURL is the same question asked of the shape the config actually
// writes. A resource names another repository by CLONE URL and by nothing else —
// there is no provider field and no numeric repository id in the vocabulary — so
// "is this ours" now arrives as a URL, in any of the spellings git accepts, and
// it has to get the answer the `owner/repo` form got.
//
// The URL is canonicalised before the comparison, so `git@github.com:Acme/w.git`
// and `https://github.com/acme/w` are one repository rather than two, and a
// string that is not a clone URL is not owned — what cannot be keyed cannot be
// vouched for.
//
// IT ANSWERS ON THE NAMESPACE, NOT ON THE HOST. Tenancy names a provider by name
// and nothing here maps a name to a host, so a URL under the owning namespace on
// some other host answers the same as one on the provider's own. That is exactly
// the reach Owns has always had; the input now carries a host this cannot yet
// use.
func (t Tenancy) OwnsCloneURL(cloneURL string) bool {
	key, err := repokey.Parse(cloneURL)
	if err != nil {
		return false
	}
	return t.owns(key.Namespace())
}

// owns is the one comparison both forms make, so neither can drift from the
// other: the owning segment against the configured namespace, case-insensitively
// because provider namespaces are, and refusing when either side is empty so an
// unconfigured cloud and a repository with no namespace both fail closed.
func (t Tenancy) owns(owner string) bool {
	if owner == "" || !t.Configured() {
		return false
	}
	return strings.EqualFold(owner, t.Owner)
}
