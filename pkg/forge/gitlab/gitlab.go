// Package gitlab drives a GitLab instance, gitlab.com, self-managed or
// loopback, as a forge.Driver; the host is the constructor's only required
// argument.
//
// GitLab nests, so Addressable accepts every depth. Access is asked over
// GraphQL, which answers 200 for failures. Push hooks exist only with Signing
// (Standard Webhooks): GitLab's older procedure replays a token and binds
// nothing. Create resolves a namespace id first. A caller's credential is
// pinned to the host and no redirect is followed (see noRedirect). The
// deployment's credential is a construction option held by the driver; without
// it the driver is a type with no membership face.
package gitlab

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// Compile-time face assertions: a face method one type wrong vanishes from
// Capabilities silently.
var (
	_ forge.Driver      = (*driver)(nil)
	_ forge.Driver      = (*memberDriver)(nil)
	_ forge.Memberships = (*memberDriver)(nil)
	_ forge.Driver      = (*signingDriver)(nil)
	_ forge.Signatures  = (*signingDriver)(nil)
	_ forge.Driver      = (*signingMemberDriver)(nil)
	_ forge.Signatures  = (*signingMemberDriver)(nil)
	_ forge.Memberships = (*signingMemberDriver)(nil)
	_ forge.PushHooks   = (*hookClient)(nil)
)

// Option configures a driver at construction. Every one of them can fail, and
// they fail at New — which is startup — rather than at the first request.
type Option func(*config) error

type config struct {
	host string
	base *url.URL

	// signing is whether the instance signs its deliveries. Without it this
	// driver has no hook faces at all. See Signing.
	signing bool

	// oauth picks which of GitLab's two credential headers is used. See OAuth.
	oauth bool

	// transport is what PinnedTransport wraps. Nil means http.DefaultTransport.
	transport http.RoundTripper

	// deployment is the credential Memberships spends. Zero means this driver
	// has no membership face at all.
	deployment forge.Credential
}

// New builds a driver for one GitLab host, a repokey host spelling. It returns
// forge.Driver because which faces a driver has is its Go type: one given a
// Deployment credential is a different type.
func New(host string, opts ...Option) (forge.Driver, error) {
	if host == "" {
		return nil, errors.New("gitlab: no host. The host is the provider, so there is no default: name the instance this driver serves")
	}
	if err := checkHostSpelling(host); err != nil {
		return nil, err
	}

	cfg := &config{host: host}
	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, err
		}
	}
	if cfg.base == nil {
		cfg.base = &url.URL{Scheme: "https", Host: host}
	}

	d := &driver{cfg: cfg}
	switch member := !cfg.deployment.IsZero(); {
	case member && cfg.signing:
		return &signingMemberDriver{memberDriver: &memberDriver{driver: d, api: cfg.httpClient(cfg.deployment)}}, nil
	case member:
		return &memberDriver{driver: d, api: cfg.httpClient(cfg.deployment)}, nil
	case cfg.signing:
		return &signingDriver{driver: d}, nil
	}
	return d, nil
}

// BaseURL points the driver at the instance's API root, for a loopback test
// instance; production derives it from the host, and its host must be the
// driver's. A path prefix is refused: it would fold into every key's owner.
func BaseURL(raw string) Option {
	return func(c *config) error {
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("gitlab: base URL %q is not a URL: %w", raw, err)
		}
		if u.Scheme != "https" && u.Scheme != "http" {
			return fmt.Errorf("gitlab: base URL %q must be http or https, not %q", raw, u.Scheme)
		}
		if strings.Trim(u.Path, "/") != "" {
			return fmt.Errorf("gitlab: base URL %q has the path prefix %q. An instance under a relative URL root spells its clone URLs with that prefix, so repokey folds the prefix into every repository's owner and this driver would resolve the wrong namespace. Serve the instance at a host root", raw, u.Path)
		}
		got, err := hostOf(u.Scheme, u.Host)
		if err != nil {
			return fmt.Errorf("gitlab: base URL %q names no usable host: %w", raw, err)
		}
		if got != c.host {
			return fmt.Errorf("gitlab: base URL %q is on host %q, but this driver serves %q; a credential would be aimed at the wrong server", raw, got, c.host)
		}
		u.RawQuery, u.Fragment, u.Path, u.RawPath = "", "", "", ""
		c.base = u
		return nil
	}
}

// Signing says the instance signs deliveries with Standard Webhooks (GitLab 19.0
// and later), which gives the driver push hooks verified under
// forge.SchemeStandardWebhooks. It fails closed: an older instance ignores the
// attribute and signs nothing, so every delivery fails verification.
func Signing() Option {
	return func(c *config) error {
		c.signing = true
		return nil
	}
}

// OAuth sends the credential as "Authorization: Bearer" instead of
// "PRIVATE-TOKEN": an OAuth access token is accepted only as Bearer, and
// forge.Credential carries no kind.
func OAuth() Option {
	return func(c *config) error {
		c.oauth = true
		return nil
	}
}

// HTTPTransport is the transport forge.PinnedTransport wraps — a proxy, a
// corporate root store, a recorder in a test. The pinning is not optional and
// this does not reach past it: whatever is supplied here still only ever sees
// requests that were already checked to be for this host.
func HTTPTransport(rt http.RoundTripper) Option {
	return func(c *config) error {
		c.transport = rt
		return nil
	}
}

// Deployment gives the driver the credential it answers membership with, a group
// access token or a service account's with read_api, held by the driver and
// reachable from no Session.
func Deployment(cred forge.Credential) Option {
	return func(c *config) error {
		if cred.IsZero() || cred.IsAnonymous() {
			return errors.New("gitlab: the deployment credential is a token; an empty or anonymous one cannot answer a membership question and must not be installed as if it could")
		}
		c.deployment = cred
		return nil
	}
}

type driver struct{ cfg *config }

func (d *driver) Host() string { return d.cfg.host }

// Addressable accepts every depth, since a GitLab group nests. It refuses a
// repository at the host root and a "-" segment, GitLab's route separator.
func (d *driver) Addressable(key repokey.Key) error { return d.cfg.addressable(key) }

// addressable is on the config because the listing asks it too.
func (c *config) addressable(key repokey.Key) error {
	// The host first: Addressable is exported and answers on its own.
	if key.Host != c.host {
		return fmt.Errorf("this driver answers for %s", c.host)
	}
	if key.Owner == "" {
		return errors.New("a GitLab project always lives in a namespace, and this key names a repository at the host root")
	}
	for _, segment := range append(strings.Split(key.Owner, "/"), key.Name) {
		if segment == "-" {
			return errors.New(`GitLab reserves the path segment "-" as a route separator, so no project is served under one`)
		}
	}
	return nil
}

func (d *driver) Bind(cred forge.Credential) (forge.Client, error) {
	if cred.IsZero() {
		return nil, forge.ErrNoCredential
	}
	return &client{cfg: d.cfg, api: d.cfg.httpClient(cred)}, nil
}

// memberDriver holds the deployment's credential, as a separate type:
// capabilities come from type assertions.
type memberDriver struct {
	*driver
	api *http.Client
}

// signingDriver is a driver for an instance that signs its deliveries: the only
// kind with hook faces, and a separate type for memberDriver's reason. Its
// clients are hookClients.
type signingDriver struct{ *driver }

func (d *signingDriver) Bind(cred forge.Credential) (forge.Client, error) {
	return bindHooks(d.driver, cred)
}

type signingMemberDriver struct{ *memberDriver }

func (d *signingMemberDriver) Bind(cred forge.Credential) (forge.Client, error) {
	return bindHooks(d.driver, cred)
}

func bindHooks(d *driver, cred forge.Credential) (forge.Client, error) {
	c, err := d.Bind(cred)
	if err != nil {
		return nil, err
	}
	return &hookClient{client: c.(*client)}, nil
}

// httpClient is one credential's http.Client. The credential is attached by
// PinnedTransport, which checks EVERY HOP: a 302 to somewhere else gets no
// header, and cleartext off loopback gets no request at all. And no hop after
// the first is ever made, because of the line above that one.
func (c *config) httpClient(cred forge.Credential) *http.Client {
	return &http.Client{CheckRedirect: noRedirect, Transport: forge.PinnedTransport(c.host, c.transport, func(h http.Header) {
		token := string(cred.Expose())
		if token == "" {
			// An anonymous session. GitLab answers public projects to nobody in
			// particular, and the core refuses to ask it an authorization
			// question, so this is a real state and not a dropped token: a
			// dropped one is forge.ErrNoCredential at Bind.
			return
		}
		if c.oauth {
			h.Set("Authorization", "Bearer "+token)
			return
		}
		h.Set("PRIVATE-TOKEN", token)
	})}
}

// This driver follows no redirect, as the github driver. GitLab answers a
// renamed or transferred project with a same-host 3xx, which the pin permits
// and Go replays, so a request for one project would be answered by another
// while returning the caller's key; a transferred path is free to re-register,
// and a write cannot be undone by checking afterwards. A renamed project
// therefore yields an error naming the 3xx.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// checkHostSpelling refuses a host that is not what repokey derives from a clone
// URL to it, so "GitLab.com", "https://gitlab.com" and "gitlab.com:443" fail at
// startup rather than becoming a driver no key ever resolves to.
func checkHostSpelling(host string) error {
	got, err := hostOf("https", host)
	if err != nil || got != host {
		return fmt.Errorf("gitlab: %q is not a host spelling repokey produces; write it lowercase, with a port only when it is not the transport's default", host)
	}
	return nil
}

// hostOf runs a scheme and authority through repokey so this package has no
// second opinion about what a host is: the default port is dropped, the case is
// folded and an IPv6 literal keeps its brackets, all by the one grammar that
// also produces the keys this driver is asked about.
func hostOf(scheme, authority string) (string, error) {
	k, err := repokey.Parse(scheme + "://" + authority + "/owner/name")
	if err != nil {
		return "", err
	}
	return k.Host, nil
}
