// Package gitea is the forge driver for the Gitea API: every Gitea, every
// Forgejo (a fork that kept the API), and codeberg.org, which is a Forgejo.
//
// ONE DRIVER, THREE NAMES, BECAUSE IT IS ONE API. Forgejo forked Gitea and kept
// /api/v1 and the webhook payload, and Codeberg runs Forgejo; a "forgejo"
// driver would be this file copied with the import path changed. What tells
// them apart is the host, and the host is a Binding's business — so adding
// Codeberg is Binding{Host: "codeberg.org", Driver: gitea.New("codeberg.org")}.
// The container suite (tag forges) runs this one driver against a real Gitea
// and a real Forgejo for exactly that reason: the claim that they are one API
// is a claim about two servers, and only both servers can make it.
//
// # Gitea is shaped like GitHub, and the differences are where it bites
//
// The API is GitHub's v3 surface with the paths moved under /api/v1, and this
// driver is the github driver's structure for that reason. Four things differ,
// and each is a place a copy would be quietly wrong:
//
//   - PAGES ARE CLAMPED BY THE SERVER. Gitea caps "limit" at its own
//     MAX_RESPONSE_ITEMS (50 by default, an operator's setting) and answers a
//     bigger ask with fewer items and no error. A pager that stops on a page
//     shorter than it asked for therefore stops after the first page on every
//     instance whose cap is below the ask — a truncated listing that reads as
//     a complete one. This pager stops on an EMPTY page, which is right under
//     any cap.
//
//   - A DEPLOY KEY SAYS WHETHER IT IS READ-ONLY, and the answer is read. A
//     key Gitea reports as writable is removed again and the install refused,
//     the gitlab driver's rule: a standing write credential is exactly what the
//     interface has no parameter for.
//
//   - MEMBERSHIP IS ONE REQUEST WITH A DEPLOYMENT TOKEN, not an app
//     installation: GET /orgs/{org}/members/{user} answers 204 or 404. A
//     caller who is not a member of the organization is REDIRECTED to the
//     public-members list rather than refused — so the redirect this driver
//     never follows is what keeps "you may not look" from reading as "not a
//     member".
//
//   - THE EVENT HEADER IS X-Gitea-Event, which Forgejo still sends beside its
//     own X-Forgejo-Event. The signature is X-Hub-Signature-256, byte for byte
//     GitHub's, so this driver names forge.SchemeHubSHA256 and verifies nothing.
//
// No SDK, for the github driver's reason: code.gitea.io/sdk would put its types
// one careless signature away from this boundary, and the whole of what tau
// asks is thirteen requests.
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

// acceptJSON is the only media type the Gitea API speaks.
const acceptJSON = "application/json"

// driver is one Gitea, constructed for the host it answers for. It carries no
// caller credential; the deployment's membership token, when there is one, is
// reachable only through memberDriver.
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

// memberDriver is a driver that also answers membership. It is a separate type
// because forge computes capabilities by type assertion, and "did the operator
// configure a membership token" is decided once, at New.
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

// Bind returns a client acting as cred and only as cred.
func (d *driver) Bind(cred forge.Credential) (forge.Client, error) {
	if cred.IsZero() {
		return nil, forge.ErrNoCredential
	}
	return d.bind(string(cred.Expose())), nil
}
