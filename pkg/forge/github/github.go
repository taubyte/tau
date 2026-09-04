// Package github is the forge driver for GitHub: github.com, and every GitHub
// Enterprise Server an operator runs.
//
// IT IS A VALUE AND NOT A CASE. Nothing here is reachable except by binding it
// to a host, and adding a second GitHub — an enterprise instance, a mirror — is
// a second Binding with a different host string. There is no provider enum
// anywhere in this package, no "github" literal on any route or in any stored
// row, and no numeric repository id in any signature: a repository is a
// repokey.Key and the API path is derived from it, which is why v1's
// "current repository" field and its numeric-id lookups have no counterpart
// here.
//
// # No SDK
//
// go-github v71 is in this module and v1 uses it, so choosing net/http needs a
// reason. It is this: go-github is in the EXPORTED signatures of three v1
// packages, which is the coupling this layer exists to end, and the whole of
// what tau asks GitHub is eleven endpoints and a dozen JSON fields. An SDK here
// would buy pagination and cost a type that must never cross the interface — a
// *github.Repository is exactly the shape forge.Repository refuses to be. What
// is left is a Link-free page counter and one status-to-taxonomy table, both of
// which are in this package where they can be read and both of which are
// tested. golang-jwt IS used, for the app assertion, because signing RS256 by
// hand would be re-implementing a wire format.
//
// # The API host is not the repository host
//
// github.com serves its API from api.github.com; an enterprise instance serves
// it from /api/v3/ on the host the repositories are on. So the host a key names
// and the host a request goes to are two values here, and PinnedTransport is
// given the SECOND. A credential for github.com is therefore pinned to
// api.github.com, which is the only host it may ever reach — a redirect
// anywhere else is refused before the header is written, not after.
//
// # What a forge says does not choose what lands in a log
//
// A non-2xx response carries a message this driver never reads. An error here
// names the method, the path this driver built and the status, and nothing
// else — because a host that can choose what lands in your logs can choose to
// put your own credential there, and an error string is the most-copied thing
// in a log. The two host-written fields that DO reach an error, a clone URL and
// a full name when a repository cannot be named, are bounded first: a host that
// picks how long a log line is picks how much of a disk it costs.
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

// driver is one GitHub, constructed for the host it answers for.
//
// It carries no caller credential. The deployment's app credential, when there
// is one, is reachable only through the Memberships face on the driver — never
// through a Session — which is what makes "a caller's token cannot answer a
// membership question" structural rather than a rule.
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

// memberDriver is a driver that also answers membership. It is a SEPARATE TYPE
// because forge computes capabilities by type assertion: a driver with no app
// credential must not appear to answer a question it would then have to answer
// with a shrug, and "did the operator configure an app" is not a thing a type
// can decide at runtime.
type memberDriver struct{ *driver }

var (
	_ forge.Driver      = (*driver)(nil)
	_ forge.Signatures  = (*driver)(nil)
	_ forge.Driver      = (*memberDriver)(nil)
	_ forge.Signatures  = (*memberDriver)(nil)
	_ forge.Memberships = (*memberDriver)(nil)
)

// Option is a deployment fact about one GitHub. There is deliberately no option
// for the API base URL: the host decides it, which is the whole doctrine, and a
// knob that let the two disagree would be a knob that aims a credential at
// another server.
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

// New builds the driver for one GitHub host.
//
// It returns forge.Driver rather than a concrete type because whether this
// deployment can answer a membership question is decided here, once, by whether
// an app credential was supplied — and that decision is a Go type.
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

// Addressable refuses what GitHub cannot address, and refuses it here so that
// the registry raises it before any request leaves the process.
//
// GITHUB TAKES EXACTLY ONE OWNER AND ONE NAME. A GitLab subgroup key has a "/"
// in its Owner, and joining those segments would build a path naming a
// repository that either does not exist or, worse, belongs to somebody else. A
// key with no owner at all is a repository served at a host's root, which
// self-hosted git does and GitHub does not.
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

// Bind returns a client acting as cred and only as cred.
func (d *driver) Bind(cred forge.Credential) (forge.Client, error) {
	if cred.IsZero() {
		return nil, forge.ErrNoCredential
	}
	return d.bind(string(cred.Expose())), nil
}
