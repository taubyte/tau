// Package gogs is the forge driver for the Gogs API, the gitea driver's
// structure for the API Gitea forked. Measured against a real Gogs (container
// suite, tag docker_integration):
//
//   - Listings are not paged: they answer whole and ignore "page", so each is one
//     request with no query.
//   - An organization's create path is singular, POST /org/{org}/repos.
//   - There is no membership question: what Gogs offers reads "could not see" as
//     "not a member", so the core answers *UnsupportedError.
//   - Removing a missing hook answers 204.
//   - The signature is X-Gogs-Signature, bare hex HMAC-SHA256
//     (forge.SchemeGogsSHA256); the event header is X-Gogs-Event.
//
// A deploy key's read-only answer is still read, the gitea driver's rule.
package gogs

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

const acceptJSON = "application/json"

// driver carries no credential.
type driver struct {
	host string
	// base is the API root, absolute and ending in "/". Gogs serves it from
	// /api/v1/ on the host the repositories are on.
	base *url.URL

	rt     http.RoundTripper // under the pin; nil means http.DefaultTransport
	scheme string            // "https", or "http" only for a loopback host
}

var (
	_ forge.Driver     = (*driver)(nil)
	_ forge.Signatures = (*driver)(nil)
)

// Option is a deployment fact about one Gogs. There is no option for the API
// base: the host decides it.
type Option func(*driver) error

// Transport is the RoundTripper UNDER the credential pin — a private CA, a
// proxy, a test server. PinnedTransport wraps it and checks every hop.
func Transport(rt http.RoundTripper) Option {
	return func(d *driver) error {
		if rt == nil {
			return errors.New("forge/gogs: Transport was given no RoundTripper")
		}
		d.rt = rt
		return nil
	}
}

// Plaintext lets the driver speak http:// to a LOOPBACK host, and only to one.
// It exists for the container suite; PinnedTransport refuses a bearer header
// over http to any other host whatever this says.
func Plaintext() Option {
	return func(d *driver) error {
		d.scheme = "http"
		return nil
	}
}

func New(host string, opts ...Option) (forge.Driver, error) {
	if host == "" {
		return nil, errors.New("forge/gogs: a driver names the host it answers for")
	}
	if k, err := repokey.Parse("https://" + host + "/owner/name"); err != nil || k.Host != host {
		return nil, fmt.Errorf("forge/gogs: %q is not a host spelling repokey produces (lowercase, and a port only when it is not the transport's default)", host)
	}
	d := &driver{host: host, scheme: "https"}
	for _, opt := range opts {
		if err := opt(d); err != nil {
			return nil, err
		}
	}
	d.base = &url.URL{Scheme: d.scheme, Host: host, Path: "/api/v1/"}
	return d, nil
}

func (d *driver) Host() string { return d.host }

// Addressable refuses what Gogs cannot address: exactly one owner and one
// name, so a nested key is refused rather than flattened.
func (d *driver) Addressable(key repokey.Key) error {
	if key.Host != d.host {
		return fmt.Errorf("this driver answers for %s", d.host)
	}
	if key.Owner == "" {
		return errors.New("Gogs serves every repository under an owner, and this key names none")
	}
	if strings.Contains(key.Owner, "/") {
		return errors.New("Gogs addresses exactly one owner and one name, and this key nests")
	}
	return nil
}

func (d *driver) Bind(cred forge.Credential) (forge.Client, error) {
	if cred.IsZero() {
		return nil, forge.ErrNoCredential
	}
	return d.bind(string(cred.Expose())), nil
}
