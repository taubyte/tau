// Package github is the forge driver for GitHub: github.com and every GitHub
// Enterprise Server. A second GitHub is a second Binding.
//
// No SDK: go-github types must never cross the interface, and tau asks eleven
// endpoints. golang-jwt signs the app assertion.
//
// github.com serves its API from api.github.com, Enterprise from /api/v3/ on the
// repository host, so the credential is pinned to the API host. A non-2xx body
// is never read: an error names the method, the path and the status, and the
// host-written strings that reach one are bounded.
package github

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
	// dotCom and its API host. Everything else is an enterprise instance, which
	// serves its API from a path on the host the repositories are on.
	dotCom    = "github.com"
	dotComAPI = "api.github.com"

	// apiVersion is the REST version header. It is pinned rather than left to
	// GitHub's default so that a behaviour change is a change to this line.
	apiVersion = "2022-11-28"
	acceptJSON = "application/vnd.github+json"
)

// The deployment's app credential is reachable only through memberDriver,
// never through a Session.
type driver struct {
	host    string
	apiHost string
	// base is the API root, absolute and ending in "/". It is a parsed URL
	// rather than a string because the routing table matches what went out
	// against it, and a second spelling of the same prefix is a spelling that
	// drifts.
	base *url.URL

	rt  http.RoundTripper // under the pin; nil means http.DefaultTransport
	app *app
}

// memberDriver also answers membership: capabilities come from type assertions,
// and whether an app credential is configured is decided at New.
type memberDriver struct{ *driver }

var (
	_ forge.Driver      = (*driver)(nil)
	_ forge.Signatures  = (*driver)(nil)
	_ forge.Driver      = (*memberDriver)(nil)
	_ forge.Signatures  = (*memberDriver)(nil)
	_ forge.Memberships = (*memberDriver)(nil)
)

// There is no option for the API base: the host decides it, and a knob letting
// the two disagree would aim a credential at another server.
type Option func(*driver) error

// Transport is the RoundTripper UNDER the credential pin — a private CA for an
// enterprise instance, a corporate proxy, a test server. It cannot be used to
// reach another host: PinnedTransport wraps it and checks every hop.
func Transport(rt http.RoundTripper) Option {
	return func(d *driver) error {
		if rt == nil {
			return errors.New("forge/github: Transport was given no RoundTripper")
		}
		d.rt = rt
		return nil
	}
}

// New returns forge.Driver because whether it answers membership is a Go type,
// decided here.
func New(host string, opts ...Option) (forge.Driver, error) {
	if host == "" {
		return nil, errors.New("forge/github: a driver names the host it answers for")
	}
	// The same check forge.New makes, made here too, so a driver built wrong is
	// wrong at its own constructor rather than at the binding that uses it.
	if k, err := repokey.Parse("https://" + host + "/owner/name"); err != nil || k.Host != host {
		return nil, fmt.Errorf("forge/github: %q is not a host spelling repokey produces (lowercase, and a port only when it is not the transport's default)", host)
	}

	// BUILT AND NOT PARSED. The host has just been through repokey, and a
	// string that is then parsed back into the value it was built from is a
	// second grammar with an error branch nothing can reach.
	d := &driver{host: host, apiHost: host, base: &url.URL{Scheme: "https", Host: host, Path: "/api/v3/"}}
	if host == dotCom {
		d.apiHost = dotComAPI
		d.base = &url.URL{Scheme: "https", Host: dotComAPI, Path: "/"}
	}
	for _, opt := range opts {
		if err := opt(d); err != nil {
			return nil, err
		}
	}
	if d.app != nil {
		return &memberDriver{d}, nil
	}
	return d, nil
}

func (d *driver) Host() string { return d.host }

// GitHub takes exactly one owner and one name: joining a subgroup's segments
// would name a repository that is missing or somebody else's. A key with no
// owner is served at a host root, which GitHub does not do.
func (d *driver) Addressable(key repokey.Key) error {
	if key.Host != d.host {
		return fmt.Errorf("this driver answers for %s", d.host)
	}
	if key.Owner == "" {
		return errors.New("GitHub serves every repository under an owner, and this key names none")
	}
	if strings.Contains(key.Owner, "/") {
		return errors.New("GitHub addresses exactly one owner and one name, and this key nests")
	}
	return nil
}

func (d *driver) Bind(cred forge.Credential) (forge.Client, error) {
	if cred.IsZero() {
		return nil, forge.ErrNoCredential
	}
	return d.bind(string(cred.Expose())), nil
}
