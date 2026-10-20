// Package gogs is the forge driver for the Gogs API.
//
// # Gogs is the API Gitea forked, and the differences are where it bites
//
// Gitea started as a fork of Gogs, so the two share /api/v1 and most of its
// paths, and this driver is the gitea driver's structure for that reason. What
// differs was measured against a real Gogs (the container suite, tag
// docker_integration), and each is a place a copy of the gitea driver would be
// quietly wrong:
//
//   - LISTINGS ARE NOT PAGED. /user/repos, a repository's keys and its hooks
//     answer the whole collection and ignore "page" and "limit": page 2 is page
//     1 again. The gitea pager stops on an EMPTY page, which here never comes —
//     it would walk a thousand copies of page 1 and yield every repository a
//     thousand times. So each listing is ONE request with no query at all.
//
//   - AN ORGANIZATION'S CREATE PATH IS SINGULAR: POST /org/{org}/repos. The
//     plural spelling Gitea uses is a 404 on Gogs.
//
//   - THERE IS NO MEMBERSHIP QUESTION. Gogs has no /orgs/{org}/members, and
//     what it does have does not answer one: /users/{user}/orgs lists only
//     PUBLIC memberships, and team rosters miss a member who was invited to the
//     organization without a team, which Gogs allows. Each would read "we
//     could not see" as "not a member", so membership is not offered and the
//     core answers *UnsupportedError.
//
//   - REMOVING A HOOK THAT DOES NOT EXIST ANSWERS 204. The success is Gogs's,
//     and it is reported as Gogs gave it: a removal is idempotent by intent.
//
//   - THE SIGNATURE IS X-Gogs-Signature, a bare hex HMAC-SHA256 with no
//     "sha256=" prefix and no X-Hub-Signature-256 beside it, so this driver
//     names forge.SchemeGogsSHA256 and verifies nothing. The event header is
//     X-Gogs-Event.
//
// Every deploy key Gogs installs is read-only, and its answer says so; the
// answer is still read, the gitea driver's rule, because a standing write
// credential is exactly what the interface has no parameter for.
//
// No SDK, for the github driver's reason: the whole of what tau asks is twelve
// requests.
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

// acceptJSON is the only media type the Gogs API speaks.
const acceptJSON = "application/json"

// driver is one Gogs, constructed for the host it answers for. It carries no
// credential at all.
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

// New builds the driver for one Gogs host.
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

// Bind returns a client acting as cred and only as cred.
func (d *driver) Bind(cred forge.Credential) (forge.Client, error) {
	if cred.IsZero() {
		return nil, forge.ErrNoCredential
	}
	return d.bind(string(cred.Expose())), nil
}
