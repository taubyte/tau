package bitbucketserver

import (
	"github.com/taubyte/tau/pkg/forge"
)

var _ forge.SignIn = (*driver)(nil)

// OAuth is an incoming application link on this Bitbucket Server or Data
// Center (8.5 and later), which serves OAuth 2.0 with PKCE.
func (d *driver) OAuth() forge.OAuth {
	at := func(path string) string { u := *d.base; u.Path = path; return u.String() }
	return forge.OAuth{
		AuthorizeURL: at("/rest/oauth2/latest/authorize"),
		TokenURL:     at("/rest/oauth2/latest/token"),
		Scopes:       []string{"REPO_ADMIN"},
		PKCE:         true,
		SecretInBody: true,
	}
}
