// Package bitbucket is the forge driver for Bitbucket Cloud, whose API is
// api.bitbucket.org/2.0; Bitbucket Server speaks another API. A credential
// bound for bitbucket.org is pinned to api.bitbucket.org, and any other host is
// taken to serve /2.0/ itself.
//
// Permission is not on the repository: /user/permissions/repositories answers
// it, and no grant on a readable repository is AccessRead only. Every deploy key
// is read-only by construction. A hook's handle is a braced UUID. A page's
// "next" URL is read for its page number only. Membership is not answered: the
// API addresses members by UUID, never by the username the contract asks
// about. A push delivery is signed X-Hub-Signature, sha256=.
package bitbucket

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

const (
	acceptJSON = "application/json"

	cloud    = "bitbucket.org"
	cloudAPI = "api.bitbucket.org"
)

type driver struct {
	host string
	// apiHost is the host every request goes to, and the one the credential is
	// pinned to.
	apiHost string
	// base is the API root, absolute and ending in "/".
	base *url.URL

	rt http.RoundTripper // under the pin; nil means http.DefaultTransport
}

var (
	_ forge.Driver     = (*driver)(nil)
	_ forge.Signatures = (*driver)(nil)
)

// Option is a deployment fact about one Bitbucket. There is no option for the
// API base: the host decides it.
type Option func(*driver) error

// Transport is the RoundTripper UNDER the credential pin — a private CA, a
// proxy, a test server. PinnedTransport wraps it and checks every hop.
func Transport(rt http.RoundTripper) Option {
	return func(d *driver) error {
		if rt == nil {
			return errors.New("forge/bitbucket: Transport was given no RoundTripper")
		}
		d.rt = rt
		return nil
	}
}

func New(host string, opts ...Option) (forge.Driver, error) {
	if host == "" {
		return nil, errors.New("forge/bitbucket: a driver names the host it answers for")
	}
	if k, err := repokey.Parse("https://" + host + "/owner/name"); err != nil || k.Host != host {
		return nil, fmt.Errorf("forge/bitbucket: %q is not a host spelling repokey produces (lowercase, and a port only when it is not the transport's default)", host)
	}
	d := &driver{host: host, apiHost: host}
	if host == cloud {
		d.apiHost = cloudAPI
	}
	d.base = &url.URL{Scheme: "https", Host: d.apiHost, Path: "/2.0/"}
	for _, opt := range opts {
		if err := opt(d); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (d *driver) Host() string { return d.host }

// Addressable refuses what Bitbucket cannot address: exactly one workspace and
// one repository slug, so a nested key is refused rather than flattened.
func (d *driver) Addressable(key repokey.Key) error {
	if key.Host != d.host {
		return fmt.Errorf("this driver answers for %s", d.host)
	}
	if key.Owner == "" {
		return errors.New("Bitbucket serves every repository under a workspace, and this key names none")
	}
	if strings.Contains(key.Owner, "/") {
		return errors.New("Bitbucket addresses exactly one workspace and one repository, and this key nests")
	}
	return nil
}

func (d *driver) Bind(cred forge.Credential) (forge.Client, error) {
	if cred.IsZero() {
		return nil, forge.ErrNoCredential
	}
	return d.bind(string(cred.Expose())), nil
}
