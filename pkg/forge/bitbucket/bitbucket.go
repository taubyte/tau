// Package bitbucket is the forge driver for Bitbucket Cloud: bitbucket.org,
// whose API is api.bitbucket.org/2.0.
//
// IT IS NOT A DRIVER FOR BITBUCKET SERVER OR DATA CENTER. Those speak
// /rest/api/1.0, a different API with different nouns (projects, not
// workspaces), and a driver for them is its own package. Bitbucket Cloud is one
// service with one API, so the host decides everything here.
//
// # The API host is not the repository host
//
// bitbucket.org serves repositories and api.bitbucket.org serves the API, the
// github driver's shape for github.com. A credential bound for bitbucket.org is
// therefore pinned to api.bitbucket.org, the only host it may ever reach. Any
// other host — a test server, a proxy in front of Bitbucket Cloud — is taken to
// serve the same /2.0/ API on itself.
//
// # Where Bitbucket differs, and where a copy of the github driver would be wrong
//
//   - PERMISSION IS NOT ON THE REPOSITORY. A repository answer says nothing
//     about what the caller may do to it; /user/permissions/repositories does,
//     filtered by full name. An empty answer for a repository the caller could
//     read is a public repository with no grant, which is AccessRead and
//     nothing more — VISIBILITY IS NOT PERMISSION.
//
//   - EVERY DEPLOY KEY IS READ-ONLY. Bitbucket Cloud has no writable deploy key
//     and no field to ask for one, so the interface's rule holds by the forge's
//     own construction rather than by a field this driver sets and reads back.
//
//   - A HOOK'S HANDLE IS A UUID IN BRACES, "{…}". It is opaque here and escaped
//     like every other segment.
//
//   - PAGES CARRY A "next" URL. This driver does not request what a forge
//     wrote: it reads the page number out of "next", refuses anything else
//     "next" says, and asks for that page through its own route table.
//
//   - MEMBERSHIP IS NOT ANSWERED. /workspaces/{w}/members/{member} addresses a
//     member by UUID or Atlassian account id; Bitbucket removed usernames from
//     its API, and the Memberships contract is a username. A driver that sent a
//     username there would read every 404 as "not a member", which is exactly
//     the answer the contract forbids an outage of meaning to produce. The face
//     is absent, so a session answers *forge.UnsupportedError.
//
//   - A PUSH DELIVERY NAMES ITS REPOSITORY BY ITS HTML LINK, not by a clone
//     URL, and carries a LIST of ref changes. It is signed X-Hub-Signature,
//     sha256= and lowercase hex — forge.SchemeBitbucketSHA256 — and verified by
//     the core, never here.
//
// No SDK: the whole of what tau asks Bitbucket is eleven requests, all of them
// in route.go.
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
	// acceptJSON is the only media type the Bitbucket API speaks.
	acceptJSON = "application/json"

	// cloud and its API host.
	cloud    = "bitbucket.org"
	cloudAPI = "api.bitbucket.org"
)

// driver is one Bitbucket Cloud, constructed for the host it answers for. It
// carries no caller credential.
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

// New builds the driver for one host.
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

// Bind returns a client acting as cred and only as cred.
func (d *driver) Bind(cred forge.Credential) (forge.Client, error) {
	if cred.IsZero() {
		return nil, forge.ErrNoCredential
	}
	return d.bind(string(cred.Expose())), nil
}
