// Package gitlab drives a GitLab instance — gitlab.com, a self-managed server,
// or a loopback one in a test — as a forge.Driver.
//
// THE HOST IS THE PROVIDER, AND THIS DRIVER IS WHAT PROVES THAT IS NOT A SLOGAN.
// Nothing here is written against "gitlab.com": the host is the constructor's
// first argument, it is the only thing a Binding names, and the same code serves
// git.acme.internal, gitlab.com and 127.0.0.1:41337. There is no provider enum
// to extend, no `provider:` field to set and no default host to fall back to —
// New with no host is an error, because a driver that guesses which server it
// talks to is a credential aimed at a server nobody named.
//
// # What GitLab makes different, and why that is the point
//
// This is the second driver over pkg/v2/forge, and it was written to find where
// that interface was still shaped like GitHub. Four things here are not GitHub's
// shape:
//
//   - GITLAB NESTS. A subgroup is a path segment, so repokey.Key.Owner routinely
//     holds a "/" and Addressable accepts every depth. This is the driver that
//     makes Addressable more than decoration: a fixed-depth forge refuses what
//     this one accepts, and neither flattens.
//
//   - AUTHORIZATION IS NOT REST. Access is asked over GraphQL, deliberately, and
//     the REST permissions hash is not merely unused but wrong — see Access.
//     GraphQL also answers 200 for failures, so the "404 is not found, 401 is the
//     credential, 5xx is an outage" mapping the taxonomy is written against does
//     not reach it, and the errors array has to be turned into the taxonomy by
//     hand.
//
//   - A WEBHOOK IS NOT SIGNED. GitLab's decade-old procedure replays a shared
//     token in a header and binds nothing — not the body, not a timestamp. It is
//     forge.SchemeSharedToken, forge.Scheme.BindsBody reports false for it, and
//     the zero forge.Policy REFUSES a driver that registers with it. That is not
//     a bug in either package: it is the interface admitting, in a value a
//     deployment must opt into, that a GitLab operator gets materially weaker
//     webhook authentication than a GitHub one.
//
//   - CREATION IS NOT A PATH OPERATION. A project is created under a NAMESPACE
//     ID, so Create resolves the namespace first. That integer is a namespace id,
//     never a repository id, it is resolved per call and stored nowhere, so the
//     doctrine's prohibition is intact — but the round trip is real and it is
//     documented on the method rather than hidden inside it.
//
// # Credentials
//
// A caller's credential is bound once, by forge.Registry.For, and spent through
// forge.PinnedTransport so it can never reach another host — and this driver
// follows NO redirect at all, because the hop a pin cannot refuse is the one
// back onto this host: see noRedirect.
//
// The DEPLOYMENT's credential — the one Memberships answers with — is a
// construction option, held by the driver, and no Session can reach it. A driver
// built without it does not implement forge.Memberships AT ALL, because it is a
// different Go type: see New.
package gitlab

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/v2/forge"
)

// Compile-time face assertions. A face method whose signature is one type wrong
// compiles clean, fails its type assertion in silence, and vanishes from
// forge.Session.Capabilities — these lines and the conformance suite's exact
// capability comparison are the two things that catch it.
var (
	_ forge.Driver      = (*driver)(nil)
	_ forge.Signatures  = (*driver)(nil)
	_ forge.Driver      = (*memberDriver)(nil)
	_ forge.Signatures  = (*memberDriver)(nil)
	_ forge.Memberships = (*memberDriver)(nil)
)

// Option configures a driver at construction. Every one of them can fail, and
// they fail at New — which is startup — rather than at the first request.
type Option func(*config) error

type config struct {
	host   string
	base   *url.URL
	scheme forge.Scheme

	// oauth picks which of GitLab's two credential headers is used. See OAuth.
	oauth bool

	// transport is what PinnedTransport wraps. Nil means http.DefaultTransport.
	transport http.RoundTripper

	// deployment is the credential Memberships spends. Zero means this driver
	// has no membership face at all.
	deployment forge.Credential
}

// New builds a driver for one GitLab host.
//
// host is a repokey host spelling — lowercase, a port only when it is not the
// transport's default — and it is what a forge.Binding must name. The same
// string is checked twice, here and in forge.New, because a host that is not
// something repokey derives from a clone URL is a driver no key will ever
// resolve to and the failure is otherwise silent forever.
//
// IT RETURNS forge.Driver AND NOT A CONCRETE TYPE ON PURPOSE. Which optional
// faces a driver has is decided by its Go type, so a face whose availability
// depends on configuration cannot be one method with a nil check inside it —
// that is precisely the stub-returning-a-zero-value the interface exists to make
// impossible. A driver given a Deployment credential is therefore a DIFFERENT
// TYPE from one without, and forge.Session.Capabilities tells the truth about
// both without either declaring anything.
func New(host string, opts ...Option) (forge.Driver, error) {
	if host == "" {
		return nil, errors.New("gitlab: no host. The host is the provider, so there is no default: name the instance this driver serves")
	}
	if err := checkHostSpelling(host); err != nil {
		return nil, err
	}

	cfg := &config{
		host: host,
		// SchemeSharedToken is what GitLab has done for a decade and what every
		// instance still does unless it was told otherwise. Defaulting to the
		// stronger scheme would mean a driver that registers hooks no ordinary
		// instance can honour, and the failure would be silent deliveries.
		scheme: forge.SchemeSharedToken,
	}
	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, err
		}
	}
	if cfg.base == nil {
		cfg.base = &url.URL{Scheme: "https", Host: host}
	}

	d := &driver{cfg: cfg}
	if cfg.deployment.IsZero() {
		return d, nil
	}
	return &memberDriver{driver: d, api: cfg.httpClient(cfg.deployment)}, nil
}

// BaseURL points the driver at the instance's API root. In production it is
// derived from the host and never set; it exists because a test instance answers
// on http at a loopback port, which is also what a developer's own GitLab does.
//
// The URL's host must be the driver's host — anything else is a credential aimed
// at another server, and PinnedTransport would refuse every request anyway, one
// layer too late to be legible.
//
// A PATH PREFIX IS REFUSED, and the reason is the doctrine rather than laziness.
// An instance served under a relative URL root spells its clone URLs with that
// prefix, so https://acme.example/gitlab/team/pay.git has the repokey key
// acme.example/gitlab/team/pay: the prefix is folded into the repository's
// OWNER. Every namespace this driver resolved would be off by one segment, and a
// tenancy scoped to "team" would be scoped to "gitlab". Give such an instance its
// own host.
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

// Signing registers new hooks under forge.SchemeStandardWebhooks instead of
// replaying a shared token, by giving the instance a signing token rather than a
// plain one.
//
// IT IS OPT-IN AND ITS FAILURE MODE IS CLOSED. Standard Webhooks signing is a
// recent GitLab feature; an instance that does not know the attribute ignores it
// and signs nothing, at which point every delivery fails verification and the
// hook is visibly broken. There is no arrangement in which getting this wrong
// causes a forged delivery to be accepted, which is why it is safe to offer at
// all. Whether an operator's instance is new enough is the operator's to know,
// and it is a per-instance fact, not a per-forge one — which is exactly why
// forge.Scheme is stored with each hook and pinned at verification rather than
// derived from the host.
func Signing() Option {
	return func(c *config) error {
		c.scheme = forge.SchemeStandardWebhooks
		return nil
	}
}

// OAuth sends the credential as "Authorization: Bearer" instead of
// "PRIVATE-TOKEN".
//
// GITLAB HAS TWO CREDENTIAL HEADERS FOR TWO CREDENTIAL KINDS, and forge.Credential
// is one opaque string with no kind, so a driver has to be told. PRIVATE-TOKEN
// carries a personal, project or group access token; an OAuth access token is only
// accepted as a Bearer. Defaulting to PRIVATE-TOKEN keeps the decade-stable case
// working with no option at all, and this is the one line a deployment adds when
// its tokens come from an OAuth flow.
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

// Deployment gives the driver the credential it answers membership questions
// with, and giving it is what makes the driver a forge.Memberships at all.
//
// It is the DEPLOYMENT's credential, not a caller's: a caller's own token
// carries whatever scopes that caller granted, and a 404 from it conflates "not
// a member" with "you may not look". Held by the driver, spent by one method,
// reachable from no Session.
//
// On GitLab this is a group access token or a service account's token with
// read_api. There is no GitHub-App equivalent — no installation, no per-tenant
// grant, no way for a tenant to revoke tau's access without revoking the token —
// and that asymmetry is a real product question rather than a coding one.
func Deployment(cred forge.Credential) Option {
	return func(c *config) error {
		if cred.IsZero() || cred.IsAnonymous() {
			return errors.New("gitlab: the deployment credential is a token; an empty or anonymous one cannot answer a membership question and must not be installed as if it could")
		}
		c.deployment = cred
		return nil
	}
}

// ─────────────────────────────── the driver ───────────────────────────────

type driver struct{ cfg *config }

func (d *driver) Host() string { return d.cfg.host }

// Addressable accepts EVERY DEPTH, and that is this driver's whole contribution
// to the shape of the interface: a GitLab group nests, so acme/team/widget is one
// repository whose owner is two segments, and a driver that joined them into
// "acme-team" or "team" would address a project that either does not exist or is
// somebody else's.
//
// What it does refuse is a key GitLab cannot name at all:
//
//   - a repository at the host root, because a project always lives in a
//     namespace, and
//   - a path segment of "-", which GitLab reserves as a route separator
//     (/acme/pay/-/settings), so no project is ever served under one.
//
// Both are refused here, before any request leaves the process, because the
// registry calls this once per key rather than each method calling it and one
// method forgetting.
func (d *driver) Addressable(key repokey.Key) error { return d.cfg.addressable(key) }

// addressable is the rule itself, on the configuration rather than on the
// driver, because the LISTING has to ask it too: a client holds the config and
// not the driver, and a listing that yielded a key this very method refuses
// would hand a consumer a project no method of this driver could ever act on.
// One rule, two callers — the alternative is a second copy that drifts.
func (c *config) addressable(key repokey.Key) error {
	// THE HOST FIRST, and it is here rather than left to the registry because
	// Addressable is EXPORTED and answers on its own. The registry routes by
	// host and Session.check compares one before calling it, so a key that
	// arrives through either has already been through it — but a consumer that
	// asks a driver directly whether it can address a key on another host was
	// being told yes. The github driver refuses it here for the same reason.
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

// memberDriver is a driver that also holds the deployment's credential. It is a
// separate type because forge computes capabilities from type assertions: a
// single type with a nil-check inside IsActiveMember would claim CapMembership
// on a deployment that cannot answer it, and would then answer "not a member" to
// a question it never asked.
type memberDriver struct {
	*driver
	api *http.Client
}

// ─────────────────────────── host and transport plumbing ───────────────────────────

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

// noRedirect stops at the 3xx instead of replaying the request wherever the
// answer points. THIS DRIVER FOLLOWS NO REDIRECT AT ALL, which is the rule the
// github driver in this same tree holds and states, and two drivers over one
// interface may not disagree about a threat the layer has written down.
//
// A PIN CANNOT SEE THIS ONE. PinnedTransport checks the HOST of every hop, and
// that is the whole of what a pin can decide: a 3xx to another path on the very
// host the credential is for passes it, and must. GitLab answers exactly that
// for a project that was renamed or TRANSFERRED, and Go then replays the
// request — as a GET on 301, 302 and 303, method and body intact on 307 and
// 308. So without this, a request built for one project is answered by another:
// the permissions read are that other project's, the hook is installed on it,
// the deploy key is installed on it, the deletion lands on it — while every
// value this driver returns still carries the key the CALLER named, because
// forge.Hook is stamped with the key that was asked about. A transferred path is
// free to be re-registered by anyone the moment it is transferred, which is what
// turns a rename into a grant.
//
// A WRITE CANNOT BE UNDONE BY CHECKING AFTERWARDS, which is why the refusal is
// before the second request rather than a re-read after it: by the time a re-fetch
// could notice, the hook is on somebody else's project and this one is gone. The
// 301/302/303 rewrite to GET is worse and not better — Delete followed it, read a
// 200, and reported success having deleted nothing.
//
// It is one rule on the client rather than a check per call because it must hold
// for every request this driver makes, the deployment credential's membership
// walk included, and because a rule with no host arithmetic in it cannot disagree
// with the pin. ErrUseLastResponse hands the 3xx to do, which reads it as the
// non-2xx it is: one error path, and the status is the fact an operator needs.
//
// THE CONSEQUENCE IS A PRODUCT DECISION AND IT IS FLAGGED RATHER THAN TAKEN
// QUIETLY: a project that was renamed or transferred now yields an error naming
// the 3xx instead of an answer about its successor. Following it is not a
// milder alternative — it is the defect above — so the choice an owner has is
// between this error and an explicit, key-comparing "follow one hop and prove
// it arrived where it was sent", which is a feature nobody has asked for. The
// github driver took this same direction on the same reasoning, and the two
// drivers agreeing is worth more than either answer alone.
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
