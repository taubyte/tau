// Package gitea is the forge driver for the Gitea API: every Gitea, every
// Forgejo, and codeberg.org. It is one API, so one driver; the host tells them
// apart. The container suite (tag forges) runs it against a real Gitea and a
// real Forgejo.
//
// It is the github driver's structure under /api/v1, except: pages are clamped
// by the server's MAX_RESPONSE_ITEMS, so the pager stops on an empty page; a
// deploy key reported writable is removed and the install refused; membership
// is one request with a deployment token, and a caller who may not look is
// redirected to the public members, which the driver never follows; and the
// event header is X-Gitea-Event, with GitHub's X-Hub-Signature-256.
package gitea

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

// The deployment's membership token is reachable only through memberDriver.
type driver struct {
	host string
	// base is the API root, absolute and ending in "/". Gitea serves it from
	// /api/v1/ on the host the repositories are on, so the API host and the
	// repository host are one value here.
	base *url.URL

	rt     http.RoundTripper // under the pin; nil means http.DefaultTransport
	scheme string            // "https", or "http" only for a loopback host
	member string            // the deployment's membership token; "" means none
}

// memberDriver also answers membership: capabilities come from type assertions,
// and whether a membership token is configured is decided at New.
type memberDriver struct{ *driver }

var (
	_ forge.Driver      = (*driver)(nil)
	_ forge.Signatures  = (*driver)(nil)
	_ forge.Driver      = (*memberDriver)(nil)
	_ forge.Signatures  = (*memberDriver)(nil)
	_ forge.Memberships = (*memberDriver)(nil)
)

// Option is a deployment fact about one Gitea. There is no option for the API
// base: the host decides it.
type Option func(*driver) error

// Transport is the RoundTripper UNDER the credential pin — a private CA, a
// proxy, a test server. PinnedTransport wraps it and checks every hop.
func Transport(rt http.RoundTripper) Option {
	return func(d *driver) error {
		if rt == nil {
			return errors.New("forge/gitea: Transport was given no RoundTripper")
		}
		d.rt = rt
		return nil
	}
}

// Membership gives the driver the DEPLOYMENT's token, the one credential that
// can answer whether a named user belongs to an organization. It must be able
// to read the organization's members — an instance admin's, or an owner's — or
// every answer is the redirect this driver reports as an error.
func Membership(token string) Option {
	return func(d *driver) error {
		if token == "" {
			return errors.New("forge/gitea: a membership credential needs its token")
		}
		d.member = token
		return nil
	}
}

// Plaintext lets the driver speak http:// to a LOOPBACK host, and only to one.
// It exists for the container suite, whose Gitea listens on 127.0.0.1 without
// TLS; PinnedTransport refuses a bearer header over http to any other host
// whatever this says, so it cannot be used to downgrade a real forge.
func Plaintext() Option {
	return func(d *driver) error {
		d.scheme = "http"
		return nil
	}
}

// New builds the driver for one Gitea host. It returns forge.Driver because
// whether it answers membership is a Go type, decided here.
func New(host string, opts ...Option) (forge.Driver, error) {
	if host == "" {
		return nil, errors.New("forge/gitea: a driver names the host it answers for")
	}
	if k, err := repokey.Parse("https://" + host + "/owner/name"); err != nil || k.Host != host {
		return nil, fmt.Errorf("forge/gitea: %q is not a host spelling repokey produces (lowercase, and a port only when it is not the transport's default)", host)
	}
	d := &driver{host: host, scheme: "https"}
	for _, opt := range opts {
		if err := opt(d); err != nil {
			return nil, err
		}
	}
	d.base = &url.URL{Scheme: d.scheme, Host: host, Path: "/api/v1/"}
	if d.member != "" {
		return &memberDriver{d}, nil
	}
	return d, nil
}

func (d *driver) Host() string { return d.host }

// Addressable refuses what Gitea cannot address. Gitea has organizations and
// users and nothing beneath them: exactly one owner and one name, like GitHub,
// so a nested key is refused rather than flattened into a name no repository
// has.
func (d *driver) Addressable(key repokey.Key) error {
	if key.Host != d.host {
		return fmt.Errorf("this driver answers for %s", d.host)
	}
	if key.Owner == "" {
		return errors.New("Gitea serves every repository under an owner, and this key names none")
	}
	if strings.Contains(key.Owner, "/") {
		return errors.New("Gitea addresses exactly one owner and one name, and this key nests")
	}
	return nil
}

func (d *driver) Bind(cred forge.Credential) (forge.Client, error) {
	if cred.IsZero() {
		return nil, forge.ErrNoCredential
	}
	return d.bind(string(cred.Expose())), nil
}
