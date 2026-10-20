package gitea

import (
	"net/url"

	"github.com/taubyte/tau/pkg/forge"
)

var _ forge.SignIn = (*driver)(nil)

// OAuth is an OAuth2 application on this Gitea or Forgejo. Neither documents
// a device flow. The scopes are Gitea 1.19's granular ones.
func (d *driver) OAuth() forge.OAuth {
	at := func(path string) string { return (&url.URL{Scheme: d.scheme, Host: d.host, Path: path}).String() }
	return forge.OAuth{
		AuthorizeURL: at("/login/oauth/authorize"),
		TokenURL:     at("/login/oauth/access_token"),
		Scopes:       []string{"write:repository", "read:user"},
		PKCE:         true,
		SecretInBody: true,
	}
}
