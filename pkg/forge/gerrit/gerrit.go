// Package gerrit is the forge driver for the Gerrit Code Review REST API.
//
// A project name is a path, "acme/team/widget" one project addressed as one
// escaped segment, so every depth is addressable. Names are case-sensitive and
// keys are not, so a project whose name is not lowercase has no key. The
// credential is "username:http-password", sent as basic auth under /a/; an
// anonymous session uses the paths without it. Every JSON body starts with the
// )]}' XSSI guard.
//
// A caller's push grant is not answerable (/access reports only is_owner, and
// check.access needs View Access), so owner is AccessAdmin, visible is
// AccessRead, and AccessWrite is never returned: the safe direction.
//
// No deploy keys (SSH keys are an account's), no push hooks or signatures (the
// webhooks plugin signs nothing; builds come from the event stream, events.go),
// and no push probe.
package gerrit

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// The deployment's membership credential is reachable only through
// memberDriver.
type driver struct {
	host   string
	rt     http.RoundTripper // under the pin; nil means http.DefaultTransport
	scheme string            // "https", or "http" only for a loopback host
	member *basic            // the deployment's credential; nil means none
	stream *streamer         // the deployment's event stream; nil means none
}

// memberDriver is a driver that also answers membership. It is a separate type
// because forge computes capabilities by type assertion, and "did the operator
// configure a membership credential" is decided once, at New.
type memberDriver struct{ *driver }

var (
	_ forge.Driver      = (*driver)(nil)
	_ forge.Driver      = (*memberDriver)(nil)
	_ forge.Memberships = (*memberDriver)(nil)
)

// basic is a username and an HTTP password. It has no String method on
// purpose and is never formatted: the only reader is the closure that writes
// the Authorization header.
type basic struct{ user, password string }

// parseToken splits "username:http-password" at the FIRST colon. A Gerrit
// username cannot contain one, and a password may.
func parseToken(token string) (*basic, error) {
	user, password, ok := strings.Cut(token, ":")
	if !ok || user == "" || password == "" {
		return nil, errors.New("forge/gerrit: a Gerrit credential is \"username:http-password\"")
	}
	return &basic{user: user, password: password}, nil
}

type Option func(*driver) error

// Transport is the RoundTripper UNDER the credential pin — a private CA, a
// proxy, a test server. PinnedTransport wraps it and checks every hop.
func Transport(rt http.RoundTripper) Option {
	return func(d *driver) error {
		if rt == nil {
			return errors.New("forge/gerrit: Transport was given no RoundTripper")
		}
		d.rt = rt
		return nil
	}
}

// Membership gives the driver the DEPLOYMENT's credential, "username:password",
// the one that answers whether a named account belongs to a group. It must be
// able to see the groups it is asked about — an administrator's, or a group
// owner's — or every answer is an error rather than a verdict.
func Membership(token string) Option {
	return func(d *driver) error {
		b, err := parseToken(token)
		if err != nil {
			return fmt.Errorf("forge/gerrit: the membership credential: %w", err)
		}
		d.member = b
		return nil
	}
}

// Plaintext lets the driver speak http:// to a LOOPBACK host, and only to one.
// PinnedTransport refuses a credential over http to any other host whatever
// this says, so it cannot be used to downgrade a real forge.
func Plaintext() Option {
	return func(d *driver) error {
		d.scheme = "http"
		return nil
	}
}

// New builds the driver for one Gerrit host. The API is served from the root
// of the host the repositories are on, so there is no option for a base path.
func New(host string, opts ...Option) (forge.Driver, error) {
	if host == "" {
		return nil, errors.New("forge/gerrit: a driver names the host it answers for")
	}
	if k, err := repokey.Parse("https://" + host + "/owner/name"); err != nil || k.Host != host {
		return nil, fmt.Errorf("forge/gerrit: %q is not a host spelling repokey produces (lowercase, and a port only when it is not the transport's default)", host)
	}
	d := &driver{host: host, scheme: "https"}
	for _, opt := range opts {
		if err := opt(d); err != nil {
			return nil, err
		}
	}
	switch {
	case d.member != nil && d.stream != nil:
		return &memberEventDriver{&memberDriver{d}, d.stream}, nil
	case d.stream != nil:
		return &eventDriver{d, d.stream}, nil
	case d.member != nil:
		return &memberDriver{d}, nil
	}
	return d, nil
}

func (d *driver) Host() string { return d.host }

// Addressable accepts every depth: a Gerrit project name is a path, so a
// nested key is a project name and not a shape to refuse.
func (d *driver) Addressable(key repokey.Key) error {
	if key.Host != d.host {
		return fmt.Errorf("this driver answers for %s", d.host)
	}
	if key.Name == "" {
		return errors.New("the key names no project")
	}
	return nil
}

func (d *driver) Bind(cred forge.Credential) (forge.Client, error) {
	if cred.IsZero() {
		return nil, forge.ErrNoCredential
	}
	if cred.IsAnonymous() {
		return d.bind(nil), nil
	}
	b, err := parseToken(string(cred.Expose()))
	if err != nil {
		return nil, err
	}
	return d.bind(b), nil
}
