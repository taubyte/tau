// Package azure is the forge driver for Azure DevOps: Services on dev.azure.com
// and Server on a host of its own. One REST API (_apis, api-version 7.1) serves
// both, so it is one driver, and which one a host runs is decided by the host.
//
// # A repository is four segments, and the key keeps all of them
//
// Azure DevOps addresses a repository as organization (a collection on Server),
// project, and repository, and serves its clone at
//
//	https://dev.azure.com/{org}/{project}/_git/{repo}
//
// so the repokey key is Owner "{org}/{project}/_git", Name "{repo}". The driver
// addresses exactly that shape and refuses every other one rather than guessing
// which segment is the project. A Server behind a virtual directory (the legacy
// /tfs/ prefix) and the legacy {org}.visualstudio.com hosts spell the path
// differently and are refused by the same rule. A project or repository whose
// name holds a space has no repokey key at all, so it is not addressable either.
//
// # The deployment names its organizations
//
// Two questions take no repository and still need an organization on the wire:
// who the credential is (connectionData) and what it can list. dev.azure.com
// serves every organization on earth, so the deployment says which ones it
// answers in, and a key in any other is refused before a request leaves.
//
// # What it cannot answer, and why
//
//   - DEPLOY KEYS. Azure DevOps has no per-repository machine key: an SSH key is
//     a user's, and reaches every repository that user can. There is nothing a
//     read-only per-repository install could map to.
//   - MEMBERSHIP. The user and entitlement APIs live on vssps.dev.azure.com and
//     vsaex.dev.azure.com. A driver's credential is pinned to its one host, so
//     answering it would mean carrying a credential to a second host.
//   - A PUSH PROBE. The driver speaks REST, not git.
//
// # It takes no push hooks, because Azure DevOps cannot sign one
//
// A push hook would be a service-hook subscription (publisher "tfs", event
// "git.push", consumer "webHooks"). Azure DevOps offers no HMAC over a
// delivery: a web hook authenticates only with HTTP basic auth or with custom
// headers, and neither binds the body or a time, so a captured or leaked
// credential lets anyone deliver any push. tau does not accept a delivery it
// cannot verify by a signature over the body, so this driver implements
// neither forge.PushHooks nor forge.Signatures and the registry reports both as
// *forge.UnsupportedError. An Azure DevOps repository is reachable through
// every other face; a build is not triggered by its pushes.
//
// No SDK, for the other drivers' reason: the whole of what tau asks is a dozen
// requests.
package azure

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// servicesHost is Azure DevOps Services. It differs from a Server in one fact
// this driver uses: its SSH endpoint is another host.
const servicesHost = "dev.azure.com"

type driver struct {
	host string
	// base is the API root, absolute and ending in "/": the host's root, which
	// every route then puts an organization under.
	base   *url.URL
	orgs   []string
	rt     http.RoundTripper
	bearer bool
}

var _ forge.Driver = (*driver)(nil)

// Option is a deployment fact about one Azure DevOps host.
type Option func(*driver) error

// Organizations names the organizations (collections, on a Server) this
// deployment answers in. At least one is required.
func Organizations(orgs ...string) Option {
	return func(d *driver) error {
		for _, o := range orgs {
			o = strings.ToLower(o)
			if o == "" || strings.Contains(o, "/") {
				return fmt.Errorf("forge/azure: %q is not an organization name", bounded(o))
			}
			if err := addressesItself(o); err != nil {
				return fmt.Errorf("forge/azure: organization: %w", err)
			}
			if !slices.Contains(d.orgs, o) {
				d.orgs = append(d.orgs, o)
			}
		}
		return nil
	}
}

// Transport is the RoundTripper UNDER the credential pin.
func Transport(rt http.RoundTripper) Option {
	return func(d *driver) error {
		if rt == nil {
			return errors.New("forge/azure: Transport was given no RoundTripper")
		}
		d.rt = rt
		return nil
	}
}

// Bearer sends a caller's credential as an OAuth bearer token (a Microsoft
// Entra access token) instead of a personal access token. A PAT goes as basic
// auth with an empty user, which is the one form every Services and Server
// release accepts.
func Bearer() Option {
	return func(d *driver) error {
		d.bearer = true
		return nil
	}
}

// New builds the driver for one Azure DevOps host.
func New(host string, opts ...Option) (forge.Driver, error) {
	if host == "" {
		return nil, errors.New("forge/azure: a driver names the host it answers for")
	}
	if k, err := repokey.Parse("https://" + host + "/owner/name"); err != nil || k.Host != host {
		return nil, fmt.Errorf("forge/azure: %q is not a host spelling repokey produces (lowercase, and a port only when it is not the transport's default)", host)
	}
	d := &driver{host: host}
	for _, opt := range opts {
		if err := opt(d); err != nil {
			return nil, err
		}
	}
	if len(d.orgs) == 0 {
		return nil, errors.New("forge/azure: a deployment names the organizations it answers in")
	}
	d.base = &url.URL{Scheme: "https", Host: host, Path: "/"}
	return d, nil
}

func (d *driver) Host() string { return d.host }

// coords are the three names a request addresses a repository by.
type coords struct{ org, project, repo string }

// Addressable refuses every shape but {org}/{project}/_git/{repo} in an
// organization this deployment answers in.
func (d *driver) Addressable(key repokey.Key) error {
	_, err := d.coords(key)
	return err
}

func (d *driver) coords(key repokey.Key) (coords, error) {
	if key.Host != d.host {
		return coords{}, fmt.Errorf("this driver answers for %s", d.host)
	}
	parts := strings.Split(key.Owner, "/")
	if len(parts) != 3 || parts[2] != "_git" || parts[0] == "" || parts[1] == "" {
		return coords{}, errors.New("Azure DevOps addresses a repository as {organization}/{project}/_git/{repository}, and this key is not that shape")
	}
	if !slices.Contains(d.orgs, parts[0]) {
		return coords{}, fmt.Errorf("organization %q is not one this deployment answers in", bounded(parts[0]))
	}
	return coords{org: parts[0], project: parts[1], repo: key.Name}, nil
}

// Bind returns a client acting as cred and only as cred.
func (d *driver) Bind(cred forge.Credential) (forge.Client, error) {
	if cred.IsZero() {
		return nil, forge.ErrNoCredential
	}
	return d.bind(string(cred.Expose())), nil
}

// authorization is the header a credential travels as.
func (d *driver) authorization(token string) string {
	if d.bearer {
		return "Bearer " + token
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(":"+token))
}
