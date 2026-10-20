// Package gerrit is the forge driver for the Gerrit Code Review REST API.
//
// GERRIT IS NOT SHAPED LIKE GITHUB, and every difference below is a place a
// copy of the gitea driver would be quietly wrong.
//
//   - A PROJECT NAME IS A PATH, NOT AN OWNER AND A NAME. "acme/team/widget" is
//     one project, addressed as one URL-encoded segment
//     (/projects/acme%2Fteam%2Fwidget), and a name with no "/" at all is a
//     project too. So this driver addresses every depth repokey can spell and
//     never splits a key into an owner and a name the API does not have.
//
//   - NAMES ARE CASE-SENSITIVE AND KEYS ARE NOT. repokey lowercases, Gerrit
//     does not, so "Acme/Pay" and "acme/pay" are two projects that would file
//     under one key. A project whose name is not already lowercase therefore
//     has no key here: it is skipped by the listing and refused when a body
//     names one, rather than filed under a key that may address another.
//
//   - THE CREDENTIAL IS A USERNAME AND AN HTTP PASSWORD, sent as HTTP basic auth
//     under the /a/ prefix, which is how Gerrit is told a request is
//     authenticated. forge.Credential holds one string, so the token is
//     "username:http-password", split once at Bind. An anonymous session uses
//     the paths without /a/ and sends no Authorization at all.
//
//   - EVERY JSON BODY STARTS WITH )]}' — Gerrit's XSSI guard — which is
//     removed before decoding.
//
//   - THE CALLER'S OWN PUSH GRANT IS NOT ANSWERABLE. GET /projects/{p}/access
//     reports is_owner and nothing about Push; check.access is refused to
//     anyone without the View Access capability (measured on 3.12.3: "view
//     access not permitted"). So the mapping is owner → AccessAdmin, visible →
//     AccessRead, and AccessWrite is never returned. That denies a contributor
//     who holds Push without ownership, which is the safe direction; reading
//     Create Reference (can_add) as write would grant somebody who may only
//     point a new ref at commits that already exist. Ownership is not Push
//     either (measured: an owner's push of a new commit is refused unless
//     Push is granted), but it is the right to write the access config that
//     grants Push, which is what AccessAdmin means.
//
// # The faces Gerrit does not have
//
// DEPLOY KEYS: Gerrit has no repository-scoped key. SSH keys belong to
// ACCOUNTS (/accounts/{id}/sshkeys), and installing one would give a builder
// everything that account may do on every project — the standing write
// credential the read-only rule exists to refuse. PUSH HOOKS AND SIGNATURES:
// core Gerrit has no webhooks; the bundled webhooks plugin
// (/config/server/webhooks~projects/{p}/remotes/{r}) posts events with NO
// secret and NO signature, so no delivery it sends can be verified by any
// forge.Scheme, and registering one would hand tau a callback anyone can
// forge. What triggers a build on Gerrit instead is the event stream —
// `gerrit stream-events` over ssh, the Events option (events.go). PUSH
// PROBE: there is no API for it; it is a git-level question. All four are left unimplemented, so a Session
// answers them with *forge.UnsupportedError.
package gerrit

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// driver is one Gerrit, constructed for the host it answers for. It carries no
// caller credential; the deployment's membership credential, when there is
// one, is reachable only through memberDriver.
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

// Option is a deployment fact about one Gerrit.
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

// Bind returns a client acting as cred and only as cred.
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
