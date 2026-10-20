package github

import (
	"net/url"

	"github.com/taubyte/tau/pkg/forge"
)

var _ forge.SignIn = (*driver)(nil)

// OAuth is an OAuth app on this GitHub: github.com's, or an Enterprise
// Server's, which serves the same paths on its own host. The device flow is
// GitHub's own and needs no client secret.
func (d *driver) OAuth() forge.OAuth {
	at := func(path string) string { return (&url.URL{Scheme: "https", Host: d.host, Path: path}).String() }
	return forge.OAuth{
		AuthorizeURL: at("/login/oauth/authorize"),
		TokenURL:     at("/login/oauth/access_token"),
		DeviceURL:    at("/login/device/code"),
		// repo and admin:repo_hook are what linking a project does with the
		// token; read:user and user:email say who it is.
		Scopes:       []string{"repo", "admin:repo_hook", "read:user", "user:email"},
		PKCE:         true,
		SecretInBody: true,
	}
}
