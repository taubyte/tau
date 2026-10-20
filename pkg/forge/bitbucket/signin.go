package bitbucket

import (
	"net/url"

	"github.com/taubyte/tau/pkg/forge"
)

var _ forge.SignIn = (*driver)(nil)

// OAuth is an OAuth consumer on Bitbucket Cloud. Its scopes are the
// consumer's, set where it is registered, so none are asked for; PKCE is not
// documented, so it is not relied on. The client secret goes as HTTP basic
// authentication, which is what Bitbucket documents.
func (d *driver) OAuth() forge.OAuth {
	at := func(path string) string { return (&url.URL{Scheme: "https", Host: d.host, Path: path}).String() }
	return forge.OAuth{
		AuthorizeURL: at("/site/oauth2/authorize"),
		TokenURL:     at("/site/oauth2/access_token"),
	}
}
