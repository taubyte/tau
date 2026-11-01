// Package azure is the forge driver for Azure DevOps, Services and Server, over
// one REST API (api-version 7.1).
//
// A key is Owner "{org}/{project}/_git", Name "{repo}"; every other shape is
// refused rather than guessed at. The deployment names the organizations it
// answers in, since dev.azure.com serves them all.
//
// It answers no deploy keys (an SSH key is a user's), no membership (those APIs
// live on hosts the credential is not pinned to), no push probe, and no push
// hooks: Azure DevOps signs no delivery, and tau accepts none it cannot verify.
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

// servicesSSHHost is where Services serves SSH, under a v3/ path that drops
// the _git segment: ssh://git@ssh.dev.azure.com/v3/{org}/{project}/{repo}.
const (
	servicesSSHHost   = "ssh.dev.azure.com"
	servicesSSHPrefix = "v3"
)

type driver struct {
	host string
	// base is the API root, absolute and ending in "/": the host's root, which
	// every route then puts an organization under.
	base   *url.URL
	orgs   []string
	rt     http.RoundTripper
	bearer bool
}

var (
	_ forge.Driver    = (*driver)(nil)
	_ forge.Spellings = (*driver)(nil)
)

// SpellingHosts is Services' SSH host. A Server serves SSH on its own host
// under the https path, which repokey already reads as the same key.
func (d *driver) SpellingHosts() []string {
	if d.host == servicesHost {
		return []string{servicesSSHHost}
	}
	return nil
}

// Canonical maps Services' SSH spelling, ssh.dev.azure.com/v3/{org}/{project}/{repo},
// onto the https key dev.azure.com/{org}/{project}/_git/{repo}. Everything
// else is not a spelling this driver rewrites.
func (d *driver) Canonical(k repokey.Key) (repokey.Key, bool) {
	if d.host != servicesHost || k.Host != servicesSSHHost {
		return k, false
	}
	parts := strings.Split(k.Owner, "/")
	if len(parts) != 3 || parts[0] != servicesSSHPrefix {
		return k, false
	}
	return repokey.Key{Host: servicesHost, Owner: parts[1] + "/" + parts[2] + "/_git", Name: k.Name}, true
}

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

type coords struct{ org, project, repo string }

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

func (d *driver) Bind(cred forge.Credential) (forge.Client, error) {
	if cred.IsZero() {
		return nil, forge.ErrNoCredential
	}
	return d.bind(string(cred.Expose())), nil
}

func (d *driver) authorization(token string) string {
	if d.bearer {
		return "Bearer " + token
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(":"+token))
}
