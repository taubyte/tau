// Package onedev is the forge driver for OneDev, whose REST API lives under
// /~api.
//
// Projects nest arbitrarily and there are no user namespaces, so every depth is
// addressed; a root project, which a key cannot spell, is refused. Every
// endpoint but one takes a numeric id, so each operation resolves the path
// first; the id never leaves the package. There is no "my permission" endpoint:
// Access asks for settings (admin), a receive-pack advertisement (write) and
// the default branch (read) in turn, a 403 being OneDev's definitive no.
//
// No push hooks: OneDev puts a hook's secret verbatim in X-OneDev-Signature and
// signs nothing. No deploy keys (an SSH key is a user's) and no membership.
package onedev

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// driver carries no credential: OneDev has nothing a deployment credential
// could answer that a caller's could not.
type driver struct {
	host string
	// base is the server root, absolute and ending in "/". The REST API is
	// under /~api/ and git under the project paths, both on this one host.
	base *url.URL

	rt     http.RoundTripper // under the pin; nil means http.DefaultTransport
	scheme string            // "https", or "http" only for a loopback host
}

var _ forge.Driver = (*driver)(nil)

// Option is a deployment fact about one OneDev. There is no option for the API
// base: the host decides it.
type Option func(*driver) error

// Transport is the RoundTripper UNDER the credential pin — a private CA, a
// proxy, a test server. PinnedTransport wraps it and checks every hop.
func Transport(rt http.RoundTripper) Option {
	return func(d *driver) error {
		if rt == nil {
			return errors.New("forge/onedev: Transport was given no RoundTripper")
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
		return nil, errors.New("forge/onedev: a driver names the host it answers for")
	}
	if k, err := repokey.Parse("https://" + host + "/owner/name"); err != nil || k.Host != host {
		return nil, fmt.Errorf("forge/onedev: %q is not a host spelling repokey produces (lowercase, and a port only when it is not the transport's default)", host)
	}
	d := &driver{host: host, scheme: "https"}
	for _, opt := range opts {
		if err := opt(d); err != nil {
			return nil, err
		}
	}
	d.base = &url.URL{Scheme: d.scheme, Host: host, Path: "/"}
	return d, nil
}

func (d *driver) Host() string { return d.host }

// Addressable refuses what OneDev cannot address. A nested owner is a parent
// project's path and is addressed; a key with no owner would be a root
// project, which a repokey key cannot name.
func (d *driver) Addressable(key repokey.Key) error {
	if key.Host != d.host {
		return fmt.Errorf("this driver answers for %s", d.host)
	}
	if key.Owner == "" {
		return errors.New("OneDev serves a repository under a parent project, and this key names none")
	}
	for _, seg := range strings.Split(key.FullName(), "/") {
		if err := addressesItself(seg); err != nil {
			return err
		}
	}
	return nil
}

func (d *driver) Bind(cred forge.Credential) (forge.Client, error) {
	if cred.IsZero() {
		return nil, forge.ErrNoCredential
	}
	return d.bind(string(cred.Expose())), nil
}
