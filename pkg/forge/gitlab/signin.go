package gitlab

import "github.com/taubyte/tau/pkg/forge"

var _ forge.SignIn = (*driver)(nil)

// OAuth is an application on this GitLab. Its device flow is GitLab 17.1's.
//
// A token from it is an OAuth token, which GitLab accepts only as a Bearer, and
// every credential this driver sends is one.
func (d *driver) OAuth() forge.OAuth {
	at := func(path string) string { u := *d.cfg.base; u.Path = path; return u.String() }
	return forge.OAuth{
		AuthorizeURL: at("/oauth/authorize"),
		TokenURL:     at("/oauth/token"),
		DeviceURL:    at("/oauth/authorize_device"),
		Scopes:       []string{"api", "read_user"},
		PKCE:         true,
		SecretInBody: true,
	}
}
