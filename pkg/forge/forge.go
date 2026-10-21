// Package forge is what tau asks a git HOST that git itself cannot answer.
//
// THE HOST IS THE PROVIDER. That is pkg/git/repokey's doctrine and this package
// does not negotiate it: a Registry maps a repokey host to a driver, and adding
// GitLab, Gitea, Codeberg or an internal mirror is registering a VALUE. There is
// no provider enum, no `provider:` field in any schema, no proto oneof, no
// switch in any consumer, and no provider-assigned repository id in any
// signature. A refusal reads "no driver for host X", never "provider Y is not
// supported".
//
// # What is mandatory is what is dangerous
//
// A Driver must say which keys it can address; a Client must answer who its
// credential is and what that credential may do to a repository. Everything
// else — listing, creating, deleting, deploy keys, hooks, clone addresses,
// namespace membership, a git-level push probe — is an OPTIONAL FACE reached
// through a Session method that returns *UnsupportedError, never a zero value.
// A new forge cannot be added by stubbing the dangerous questions; it can only
// be added by answering them. A stub that returns a zero value is a security
// defect and not an inconvenience: v1's `if client != nil` guards are exactly
// that, and on its p2p path they skip the authorization check outright.
//
// # A driver never verifies a signature
//
// Verification is a closed set of named Schemes implemented once in this
// package — which header, which encoding, exactly which bytes are covered,
// which MAC, what a match means. A driver's whole contribution is NAMING one
// and reading a payload it is handed AFTER the core verified it. GitHub, Gitea,
// Forgejo and Codeberg sign identically, so they name the same scheme and
// contain zero verification code between them; a forge that signs a new way
// adds a scheme here, beside the externally-computed vectors and the negative
// cases. See webhook.go.
//
// # Three answers, never two
//
// "No" and "I could not ask" are different, and an outage must never read as a
// refusal. A definitive negative is a value: (AccessNone, nil), (false, nil).
// Everything else is an error, and the errors are typed so a caller cannot
// collapse them by accident. See errors.go.
//
// # Nothing from a forge SDK crosses this boundary
//
// No *github.User, no *github.Repository, no escape hatch returning one, and no
// provider-assigned repository id in any argument or return. The one
// provider-assigned value that survives is a Handle — the id a forge gave to a
// resource IT created and owns, a webhook or a deploy key — and it is an opaque
// string because Bitbucket's is a UUID where three others number theirs.
//
// This package imports exactly one package outside the standard library,
// pkg/git/repokey, which is itself stdlib-only so a leaf builds under GOOS=js.
// Drivers sit ABOVE it and bring their own SDKs; nothing here does.
package forge

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// Capability names one question. It is not a provider name, nothing selects a
// driver by it, and it never appears in config, on a route or on a wire: it
// exists so a refusal can say which question went unanswered.
type Capability string

const (
	CapIdentity   Capability = "identity"   // mandatory
	CapAccess     Capability = "access"     // mandatory
	CapRepository Capability = "repository" // mandatory
	CapList       Capability = "list"
	CapCreate     Capability = "create"
	CapDelete     Capability = "delete"
	CapDeployKeys Capability = "deploy-keys"
	CapPushHooks  Capability = "push-hooks"
	CapCloneAddr  Capability = "clone-address"
	CapPushProbe  Capability = "push-probe"
	CapMembership Capability = "membership"
	CapSignatures Capability = "signatures"
)

// Set is what a session can answer. It is COMPUTED BY THIS PACKAGE from type
// assertions and never declared by a driver, so "declared but not implemented"
// and "implemented but not declared" are both unrepresentable — a driver can
// neither over-claim nor under-claim.
type Set map[Capability]struct{}

func (s Set) Has(c Capability) bool { _, ok := s[c]; return ok }

// List is the sorted members, so a set has one rendering and a test can compare
// it to a literal.
func (s Set) List() []Capability {
	out := make([]Capability, 0, len(s))
	for c := range s {
		out = append(out, c)
	}
	slices.Sort(out)
	return out
}

func (s Set) String() string {
	parts := make([]string, 0, len(s))
	for _, c := range s.List() {
		parts = append(parts, string(c))
	}
	return strings.Join(parts, ",")
}

// Equal is set equality. The conformance suite compares a session's computed
// set against a declared one with it, because a face whose method signature is
// one character wrong compiles clean and silently disappears from the set.
func (s Set) Equal(other Set) bool {
	if len(s) != len(other) {
		return false
	}
	for c := range s {
		if !other.Has(c) {
			return false
		}
	}
	return true
}

// ─────────────────────────────── the driver ───────────────────────────────

// Driver is one forge's SOFTWARE, constructed for the host it serves so that
// New can refuse a binding aiming a credential at another server. It holds no
// caller credential; a deployment credential (see Memberships) is baked in at
// construction and is reachable only through the driver, never through a
// Session.
type Driver interface {
	// Host is the repokey.Key.Host spelling this driver answers for:
	// lowercased, a port only when it is not the transport's default.
	Host() string

	// Addressable reports why this driver cannot address key, or nil.
	//
	// A DRIVER REFUSES A DEPTH IT CANNOT ADDRESS; IT NEVER FLATTENS ONE.
	// GitLab nests subgroups, so Owner routinely holds a "/"; GitHub, Gitea
	// and Bitbucket take exactly one owner and one name. Joining segments into
	// a name no repository has is how a request ends up addressing something
	// other than what the caller named. The REGISTRY calls this once, before
	// any request, rather than each driver calling it in each method where one
	// will forget.
	Addressable(key repokey.Key) error

	// Bind returns a client acting as cred and ONLY as cred. It must refuse a
	// zero Credential with ErrNoCredential.
	Bind(cred Credential) (Client, error)
}

// Client is a driver acting as one credential. IT IS STATELESS: every method
// takes the key it acts on, because a client carrying a "current repository" is
// a client whose other methods silently depend on a fetch having happened
// first.
//
// Consumers do not hold a Client. They hold a *Session, which the registry
// returns and which owns every refusal.
type Client interface {
	// Host is the host this client acts on, and it must be its driver's. The
	// registry checks it at bind time: a client answering for another host is
	// a credential aimed at the wrong server, one layer below the binding.
	Host() string

	// Whoami says who this credential is. It doubles as the only honest
	// token-validity check a forge offers.
	Whoami(ctx context.Context) (Identity, error)

	// Access is THE authorization question and the reason this package exists.
	// A definitive answer is (level, nil), INCLUDING (AccessNone, nil), which
	// means the forge was asked and said no. Anything else is an error, so an
	// outage can never read as a refusal.
	//
	// VISIBILITY IS NOT PERMISSION: a public repository the caller holds no
	// grant on is AccessRead, not AccessWrite.
	Access(ctx context.Context, key repokey.Key) (Access, error)

	// Repository is what only the forge knows. Key must be DERIVED from the
	// clone URL the forge returned, not echoed back from the argument.
	Repository(ctx context.Context, key repokey.Key) (Repository, error)
}

// ──────────────────── optional faces, implemented by a Client ────────────────────

// Lister yields the repositories this credential can reach. It yields rather
// than returning a slice because every forge pages and none bounds the result,
// and IT TAKES NO OPTIONS because the affiliation vocabulary
// ("owner,collaborator,organization_member") is GitHub's and has no cross-forge
// meaning. The contract is the weakest true one: everything yielded is readable
// by this credential, and AN ABSENCE PROVES NOTHING — Bitbucket's own listing
// omits public repositories the caller was never explicitly granted.
//
// Returning false from yield stops the iteration and is not an error.
type Lister interface {
	ListRepositories(ctx context.Context, yield func(Repository) bool) error
}

// Creator creates a repository AT THE KEY IT WILL BE ADDRESSED BY, including
// its namespace. Creating inside a namespace is a real requirement v1 cannot
// express: both of its create paths pass an empty org, so it can only ever
// create under the authenticated account even though its tenancy names one. A
// driver that cannot create in a namespace REFUSES a key naming one, rather
// than quietly creating the repository somewhere else.
type Creator interface {
	Create(ctx context.Context, key repokey.Key, opts CreateOptions) (Repository, error)
}

// CreateOptions is the intersection four forges agree on. Anything a single
// forge adds — an issue tracker, a wiki, a default license — is that forge's
// default and not tau's business.
type CreateOptions struct {
	Description string
	Private     bool
}

// Refusal is a driver error that is the forge's own answer refusing a
// request — a 4xx — as opposed to a failure to get an answer at all.
type Refusal interface {
	error
	Refused() bool
}

// Deleter deletes a repository. It exists because it is the consumer of
// AccessAdmin: v1's CLI discovers that it lacked admin by string-matching an
// error body.
type Deleter interface {
	Delete(ctx context.Context, key repokey.Key) error
}

// DeployKeys installs and removes machine clone credentials.
//
// EVERY KEY THIS INSTALLS IS READ-ONLY AND THERE IS NO PARAMETER TO CHANGE
// THAT. tau's deploy key exists so a builder can clone: nothing under services/
// pushes with one. v1 installs a WRITE key anyway, because it sets only a title
// and a key and its SDK's zero value for read-only is false — and since nothing
// removes one either, every repository it has registered carries a standing
// write credential it issued and forgot. Making the choice unspeakable rather
// than defaulted is the difference between that being a bug and being
// impossible. A future need to push is a new method whose name says so.
//
// publicKey is the authorized_keys-format public half. The private half is the
// consumer's and never enters this package.
type DeployKeys interface {
	InstallDeployKey(ctx context.Context, key repokey.Key, title, publicKey string) (Handle, error)
	RemoveDeployKey(ctx context.Context, key repokey.Key, h Handle) error

	// ListDeployKeys is every deploy key on the repository, whoever installed
	// it. It is how a consumer finds the key IT installed earlier — by the
	// title or public half it chose — so that replacing one removes the old.
	ListDeployKeys(ctx context.Context, key repokey.Key) ([]InstalledDeployKey, error)
}

// InstalledDeployKey is one deploy key a forge holds. PublicKey is the
// authorized_keys form without a comment, which is all every forge keeps.
type InstalledDeployKey struct {
	Handle    Handle
	Title     string
	PublicKey string
}

// PushHooks registers and removes push webhooks.
//
// THE DRIVER NEVER SEES THE KEY MATERIAL. It is handed `wire` — the already
// encoded string this hook's scheme says the forge should be given, derived by
// Scheme.Wire from a Secret the driver cannot reach. So a driver cannot mint a
// weak secret, keep one, log one, or encode one differently from the way the
// verifier will key its MAC: the encoding a forge is given and the key an HMAC
// is computed with are two halves of one decision, made together, in one
// function.
//
// Reached only through Session.RegisterPushHook, which is what makes "a hook
// whose scheme nothing recorded" unconstructible.
type PushHooks interface {
	RegisterPushHook(ctx context.Context, key repokey.Key, callback *url.URL, wire string) (Handle, error)
	RemovePushHook(ctx context.Context, key repokey.Key, h Handle) error

	// ListPushHooks is every webhook on the repository, whoever registered it,
	// and only what a forge says about one without its secret: the handle and
	// where it delivers. It is how a consumer finds a hook IT registered
	// earlier, by its callback.
	ListPushHooks(ctx context.Context, key repokey.Key) ([]InstalledPushHook, error)
}

// InstalledPushHook is one webhook a forge holds.
type InstalledPushHook struct {
	Handle   Handle
	Callback string
}

// CloneAddresses answers "what address does a clone of this key use on this
// host, and what account does it authenticate as". It is a forge question and
// not a formatting one, and it is what retires pkg/git's "github.com" literal
// and its second URL grammar.
type CloneAddresses interface {
	CloneAddress(key repokey.Key, over Protocol) (Address, error)
}

// PushProber answers a DIFFERENT QUESTION FROM Access, and the separation is
// the point. It reports whether THE HOLDER OF A GIT CREDENTIAL may push to a
// remote, proven by a git-receive-pack reference advertisement, which
// authenticates and authorizes without transferring an object. There is no user
// in that answer: on a bare remote the credential is a machine key, possibly
// the very deploy key tau installed on that repository. A consumer that accepts
// it as a substitute for Access has to WRITE THAT SUBSTITUTION DOWN — a
// different method, a different return type, a different argument — where a
// reviewer can find it.
type PushProber interface {
	CanPush(ctx context.Context, key repokey.Key, sshPrivateKey []byte) (bool, error)
}

// ──────────────────── optional faces, implemented by a Driver ────────────────────

// Memberships answers whether a named human is an active member of a namespace.
//
// IT HANGS OFF THE DRIVER AND NOT OFF A CLIENT, AND THAT PLACEMENT IS THE
// POINT. It is the one question a caller's own credential cannot answer: a user
// token carries whatever scopes the user granted, and a 404 conflates "not a
// member" with "you may not look". It is answered with a credential the
// DEPLOYMENT holds, baked into the driver at construction — so a caller-bound
// session has no path to that credential, and that credential has no path to
// acting as a caller. Structural, not a rule.
//
// THE CONTRACT IS v1'S, UNCHANGED: (false, nil) is a definitive non-member and
// everything else is an error, so an outage can never read as a refusal.
type Memberships interface {
	IsActiveMember(ctx context.Context, namespace, username string) (bool, error)
}

// Signatures is what a driver contributes to webhooks, and NEITHER HALF IS THE
// VERDICT.
type Signatures interface {
	// Scheme names the procedure this driver registers NEW hooks with. It does
	// not implement one. A driver whose forge offers two decides at
	// construction which it registers with; verification always dispatches on
	// the scheme STORED with the hook, so an instance upgraded later keeps
	// verifying its older hooks correctly.
	Scheme() Scheme

	// Push reads a delivery the core HAS ALREADY VERIFIED. The parameter type
	// is the enforcement: VerifiedBody's fields are unexported, so no package
	// outside this one can construct a non-empty one, and there is therefore
	// no path by which unchecked bytes reach a parser.
	//
	// It returns a CLAIM — what the payload says — and the core compares the
	// repository it claims against the one the hook was registered for.
	// Returns ErrNotAPush for a delivery that is not a push.
	Push(body VerifiedBody, header http.Header) (PushClaim, error)
}

// Spellings is how a forge says that several of its own addresses name ONE
// repository. repokey has one grammar and keeps only host and path, which is
// right for every forge serving https and ssh on one host under one path. Some
// do not: a forge serving https under a path prefix and ssh on another port,
// or ssh on another host under another path, hands out two clone addresses
// that repokey reads as two repositories. The forge knows its own spellings,
// so the forge says so, here, and repokey stays free of any forge's vocabulary.
//
// It hangs off the Driver because it is a fact about the forge's software and
// deployment, not about a caller's credential, and it is consulted through
// Registry.Key, the one function that turns an address into a key.
type Spellings interface {
	// SpellingHosts are the repokey hosts, OTHER than Host, this forge serves
	// clone addresses on — an ssh endpoint on a port or a host of its own. The
	// registry binds each to this driver for spelling alone: no credential is
	// ever bound to one, and Hosts does not list them.
	SpellingHosts() []string

	// Canonical maps a key parsed from one of this forge's addresses, on Host
	// or on a SpellingHosts host, to the key the repository is filed under. It
	// must be idempotent: a canonical key maps to itself. ok is false for a key
	// this forge does not spell, and the parse then stands as it is.
	Canonical(parsed repokey.Key) (canonical repokey.Key, ok bool)
}

// ─────────────────────────────── the registry ───────────────────────────────

// Binding is the WHOLE of "how a forge is added": a value, handed to New.
// Adding Codeberg is Binding{Host: "codeberg.org", Driver: gitea.New(...)};
// adding a self-hosted mirror is the same line with a different host. Nothing
// downstream gains a case.
type Binding struct {
	Host   string
	Driver Driver
}

// Policy is what a deployment accepts. THE ZERO VALUE IS THE STRICT ONE, so
// every loosening is opt-in and visible at one call site.
type Policy struct {
	// AllowInsecureCallback accepts a non-https webhook callback URL. The
	// callback is where a forge sends a signed delivery; over plain http
	// anyone on the path reads every payload.
	AllowInsecureCallback bool

	// MaxDeliveryBody caps a delivery before any MAC is computed over it. Zero
	// means DefaultMaxDeliveryBody.
	MaxDeliveryBody int

	// Now and Tolerance bound a timestamped scheme's replay window. Zero means
	// time.Now and DefaultTimestampTolerance.
	Now       func() time.Time
	Tolerance time.Duration
}

// Registry maps a host to the driver that serves it. IT IS IMMUTABLE AFTER
// CONSTRUCTION — there is no Register method — so there is no moment at which a
// lookup races a rebinding, and no path by which anything running can
// substitute the driver for a host.
type Registry struct {
	drivers map[string]Driver
	// spellings binds each SpellingHosts host to the driver that spells it.
	// Key consults it; nothing that binds a credential does.
	spellings map[string]Driver
	policy    Policy
}

// New refuses, AT STARTUP, a deployment that is wrong in a way that would
// otherwise surface only as a security failure:
//
//   - a binding whose host disagrees with its driver's own Host() — a
//     credential aimed at the wrong server
//   - two bindings for one host — an ambiguity someone would otherwise resolve
//     by map iteration order
//   - a host that is not a repokey host spelling, so a key would never match it
//   - a driver naming a scheme this package does not implement
//   - a spelling host that is not a repokey host spelling, that is bound to a
//     driver of its own, or that two drivers claim — one address would name
//     two repositories, decided by map order
func New(policy Policy, bindings ...Binding) (*Registry, error) {
	r := &Registry{drivers: make(map[string]Driver, len(bindings)), spellings: map[string]Driver{}, policy: policy}
	for _, b := range bindings {
		if b.Driver == nil {
			return nil, fmt.Errorf("forge: binding for host %q has no driver", b.Host)
		}
		if b.Host == "" {
			return nil, fmt.Errorf("forge: binding for driver %T names no host", b.Driver)
		}
		if err := validHost(b.Host); err != nil {
			return nil, err
		}
		if dh := b.Driver.Host(); dh != b.Host {
			return nil, fmt.Errorf("forge: binding host %q disagrees with driver host %q; a credential would be aimed at the wrong server", b.Host, dh)
		}
		if _, taken := r.drivers[b.Host]; taken {
			return nil, fmt.Errorf("forge: two drivers bound to host %q", b.Host)
		}
		if s, ok := b.Driver.(Signatures); ok {
			sch := s.Scheme()
			if !sch.implemented() {
				return nil, fmt.Errorf("forge: driver for %q registers hooks with scheme %q, which this package does not implement", b.Host, sch)
			}
			if sch == SchemePayloadEd25519 {
				pk, ok := b.Driver.(PayloadKey)
				if !ok || len(pk.PayloadKey()) != ed25519.PublicKeySize {
					return nil, fmt.Errorf("forge: driver for %q registers hooks with scheme %q and names no Ed25519 key the forge signs with", b.Host, sch)
				}
			}
		}
		r.drivers[b.Host] = b.Driver
	}
	// After every binding, so a spelling host colliding with a bound host is
	// refused whichever order the bindings came in.
	for _, b := range bindings {
		sp, ok := b.Driver.(Spellings)
		if !ok {
			continue
		}
		for _, h := range sp.SpellingHosts() {
			if err := validHost(h); err != nil {
				return nil, err
			}
			if _, bound := r.drivers[h]; bound {
				return nil, fmt.Errorf("forge: driver for %q spells addresses on %q, which is bound to a driver of its own", b.Host, h)
			}
			if other, taken := r.spellings[h]; taken && other != b.Driver {
				return nil, fmt.Errorf("forge: two drivers spell addresses on host %q", h)
			}
			r.spellings[h] = b.Driver
		}
	}
	return r, nil
}

// Key is THE function that turns a clone address into a repository key:
// repokey.Parse, and then — when the parsed host is one a driver serves or
// spells addresses on, and that driver implements Spellings — the driver's
// canonical key. A URL no driver claims keeps exactly repokey's parse, and so
// does every URL on a nil Registry, so a consumer holding none still speaks the
// one grammar.
//
// A canonical key on a host other than the driver's own is refused: a
// spelling maps addresses onto the forge serving them, never onto another.
func (r *Registry) Key(cloneURL string) (repokey.Key, error) {
	k, err := repokey.Parse(cloneURL)
	if err != nil || r == nil {
		return k, err
	}
	d, ok := r.drivers[k.Host]
	if !ok {
		if d, ok = r.spellings[k.Host]; !ok {
			return k, nil
		}
	}
	sp, ok := d.(Spellings)
	if !ok {
		return k, nil
	}
	c, ok := sp.Canonical(k)
	if !ok {
		return k, nil
	}
	if c.Host != d.Host() {
		return repokey.Key{}, fmt.Errorf("forge: driver for %q spelled an address on %q as a key on %q", d.Host(), k.Host, c.Host)
	}
	return c, nil
}

// validHost checks that a host string is the spelling repokey produces, so a
// binding for "GitHub.com" or "https://github.com" fails at startup rather than
// never matching a key and reading, forever, as a host nobody registered.
func validHost(host string) error {
	k, err := repokey.Parse("https://" + host + "/owner/name")
	if err != nil || k.Host != host {
		return fmt.Errorf("forge: %q is not a host spelling repokey produces (lowercase, port only when non-default)", host)
	}
	return nil
}

// Driver resolves a host. A miss is a *NoDriverError and never a default.
func (r *Registry) Driver(host string) (Driver, error) {
	d, ok := r.drivers[host]
	if !ok {
		return nil, &NoDriverError{Host: host}
	}
	return d, nil
}

// Hosts is what this registry can answer for, sorted, for a startup log line.
func (r *Registry) Hosts() []string {
	out := make([]string, 0, len(r.drivers))
	for h := range r.drivers {
		out = append(out, h)
	}
	slices.Sort(out)
	return out
}

// For resolves the driver for a key's host, checks that the driver can address
// the key, and binds a credential — in ONE place, before any request. Every
// per-key call goes through here, so the depth refusal happens once rather than
// in each method of each driver.
func (r *Registry) For(key repokey.Key, cred Credential) (*Session, error) {
	d, err := r.Driver(key.Host)
	if err != nil {
		return nil, err
	}
	if err := d.Addressable(key); err != nil {
		return nil, &UnaddressableError{Host: key.Host, Key: key, Reason: err.Error()}
	}
	return r.bind(d, cred)
}

// Host binds a credential to a host without naming a repository — what a
// listing or a creation starts from.
func (r *Registry) Host(host string, cred Credential) (*Session, error) {
	d, err := r.Driver(host)
	if err != nil {
		return nil, err
	}
	return r.bind(d, cred)
}

func (r *Registry) bind(d Driver, cred Credential) (*Session, error) {
	if cred.IsZero() {
		return nil, ErrNoCredential
	}
	c, err := d.Bind(cred)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("forge: driver for %q returned a nil client", d.Host())
	}
	// A client that answers for another host is a credential aimed at the wrong
	// server, one layer below the binding New already checks. It is checked
	// here rather than trusted because this is the only place a Client is ever
	// built, and because it is the one thing Client.Host is for.
	if ch := c.Host(); ch != d.Host() {
		return nil, &CredentialLeakError{Bound: d.Host(), To: ch}
	}
	return &Session{reg: r, driver: d, client: c, anonymous: cred.IsAnonymous()}, nil
}

// IsActiveMember returns *UnsupportedError — NEVER false — for a host whose
// driver has no membership face. "We could not ask" must not arrive at a caller
// as "they are not a member", because the caller will act on it.
//
// It hangs off the Registry rather than a Session because the credential that
// answers it is the deployment's, held by the driver, and a caller-bound
// session must have no path to it.
func (r *Registry) IsActiveMember(ctx context.Context, host, namespace, username string) (bool, error) {
	d, err := r.Driver(host)
	if err != nil {
		return false, err
	}
	m, ok := d.(Memberships)
	if !ok {
		return false, &UnsupportedError{Host: host, Capability: CapMembership}
	}
	return m.IsActiveMember(ctx, namespace, username)
}

// ─────────────────────────────── the session ───────────────────────────────

// Session is a forge as seen through one credential, and it is CONCRETE so
// every refusal is written once here rather than once per driver. A driver
// cannot invent a second way to say no, cannot forget the anonymous guard, and
// cannot be handed a key on another host.
//
// CAPABILITY IS A PROPERTY OF (FORGE, CREDENTIAL) AND NOT OF THE FORGE ALONE.
// That is why membership is not reachable from here at all: GitHub can answer
// it, but not from a caller's token.
type Session struct {
	reg       *Registry
	driver    Driver
	client    Client
	anonymous bool
}

func (s *Session) Host() string      { return s.driver.Host() }
func (s *Session) IsAnonymous() bool { return s.anonymous }

// Capabilities is COMPUTED from type assertions on the bound client and its
// driver, so a driver can neither over- nor under-claim. The failure it is
// built against is real: a face method whose signature is one type wrong
// compiles clean and silently drops out of this set, and nothing but comparing
// the set to a declared one finds it.
func (s *Session) Capabilities() Set {
	set := Set{CapIdentity: {}, CapAccess: {}, CapRepository: {}}
	if _, ok := s.client.(Lister); ok {
		set[CapList] = struct{}{}
	}
	if _, ok := s.client.(Creator); ok {
		set[CapCreate] = struct{}{}
	}
	if _, ok := s.client.(Deleter); ok {
		set[CapDelete] = struct{}{}
	}
	if _, ok := s.client.(DeployKeys); ok {
		set[CapDeployKeys] = struct{}{}
	}
	if _, ok := s.client.(PushHooks); ok {
		set[CapPushHooks] = struct{}{}
	}
	if _, ok := s.client.(CloneAddresses); ok {
		set[CapCloneAddr] = struct{}{}
	}
	if _, ok := s.client.(PushProber); ok {
		set[CapPushProbe] = struct{}{}
	}
	if _, ok := s.driver.(Signatures); ok {
		set[CapSignatures] = struct{}{}
	}
	if _, ok := s.driver.(Memberships); ok {
		set[CapMembership] = struct{}{}
	}
	if _, ok := s.driver.(PushEvents); ok {
		set[CapPushEvents] = struct{}{}
	}
	if _, ok := s.driver.(SignIn); ok {
		set[CapSignIn] = struct{}{}
	}
	return set
}

// Require is ask-before-you-rely FOR A WHOLE FLOW. A registration that needs a
// hook and a deploy key checks both before it installs either, instead of
// discovering the second is impossible after the first has already landed on
// someone's repository — a side effect nothing in this tree removes.
func (s *Session) Require(caps ...Capability) error {
	have := s.Capabilities()
	for _, c := range caps {
		if !have.Has(c) {
			return &UnsupportedError{Host: s.Host(), Capability: c}
		}
	}
	return nil
}

// check is the per-key guard every keyed method runs. It is here and not in a
// driver because "the key is on my host" and "the key is a shape I can address"
// are the two mistakes that put a request on the wrong repository.
func (s *Session) check(key repokey.Key) error {
	if key.Host != s.Host() {
		return &WrongHostError{Session: s.Host(), Key: key}
	}
	if err := s.driver.Addressable(key); err != nil {
		return &UnaddressableError{Host: s.Host(), Key: key, Reason: err.Error()}
	}
	return nil
}

func (s *Session) Whoami(ctx context.Context) (Identity, error) {
	if s.anonymous {
		return Identity{}, ErrAnonymous
	}
	return s.client.Whoami(ctx)
}

// Access refuses on an anonymous session HERE, in the core, so no driver can
// forget it.
func (s *Session) Access(ctx context.Context, key repokey.Key) (Access, error) {
	if s.anonymous {
		return AccessNone, ErrAnonymous
	}
	if err := s.check(key); err != nil {
		return AccessNone, err
	}
	return s.client.Access(ctx, key)
}

func (s *Session) Repository(ctx context.Context, key repokey.Key) (Repository, error) {
	if err := s.check(key); err != nil {
		return Repository{}, err
	}
	return s.client.Repository(ctx, key)
}

func (s *Session) ListRepositories(ctx context.Context, yield func(Repository) bool) error {
	l, ok := s.client.(Lister)
	if !ok {
		return &UnsupportedError{Host: s.Host(), Capability: CapList}
	}
	return l.ListRepositories(ctx, yield)
}

func (s *Session) Create(ctx context.Context, key repokey.Key, opts CreateOptions) (Repository, error) {
	c, ok := s.client.(Creator)
	if !ok {
		return Repository{}, &UnsupportedError{Host: s.Host(), Capability: CapCreate}
	}
	if err := s.check(key); err != nil {
		return Repository{}, err
	}
	made, err := c.Create(ctx, key, opts)
	if err != nil {
		// EVERY FORGE SAYS "TAKEN" IN ITS OWN WORDS, and none of them is
		// parsed: the repository being there afterwards is the one answer
		// that reads the same on all of them.
		var refused Refusal
		if errors.As(err, &refused) && refused.Refused() {
			if there, rerr := s.client.Repository(ctx, key); rerr == nil && there.Key == key {
				return Repository{}, &ExistsError{Host: s.Host(), Key: key}
			}
		}
		return Repository{}, err
	}
	return made, nil
}

func (s *Session) Delete(ctx context.Context, key repokey.Key) error {
	d, ok := s.client.(Deleter)
	if !ok {
		return &UnsupportedError{Host: s.Host(), Capability: CapDelete}
	}
	if err := s.check(key); err != nil {
		return err
	}
	return d.Delete(ctx, key)
}

func (s *Session) InstallDeployKey(ctx context.Context, key repokey.Key, title, publicKey string) (Handle, error) {
	k, ok := s.client.(DeployKeys)
	if !ok {
		return "", &UnsupportedError{Host: s.Host(), Capability: CapDeployKeys}
	}
	if err := s.check(key); err != nil {
		return "", err
	}
	if strings.TrimSpace(publicKey) == "" {
		return "", fmt.Errorf("forge: deploy key for %q has no public half", key)
	}
	return k.InstallDeployKey(ctx, key, title, publicKey)
}

func (s *Session) ListDeployKeys(ctx context.Context, key repokey.Key) ([]InstalledDeployKey, error) {
	k, ok := s.client.(DeployKeys)
	if !ok {
		return nil, &UnsupportedError{Host: s.Host(), Capability: CapDeployKeys}
	}
	if err := s.check(key); err != nil {
		return nil, err
	}
	return k.ListDeployKeys(ctx, key)
}

func (s *Session) RemoveDeployKey(ctx context.Context, key repokey.Key, h Handle) error {
	k, ok := s.client.(DeployKeys)
	if !ok {
		return &UnsupportedError{Host: s.Host(), Capability: CapDeployKeys}
	}
	if err := s.check(key); err != nil {
		return err
	}
	return k.RemoveDeployKey(ctx, key, h)
}

// RegisterPushHook is the only way a hook is created, and it is what makes the
// scheme a hook was registered under and the scheme it will be verified under
// the same value BY CONSTRUCTION: the registry derives the wire form from the
// driver's own Scheme() and stamps the returned Hook with that same scheme. The
// driver receives `wire` and never the Secret.
func (s *Session) RegisterPushHook(ctx context.Context, key repokey.Key, callback *url.URL, secret Secret) (Hook, error) {
	h, ok := s.client.(PushHooks)
	if !ok {
		return Hook{}, &UnsupportedError{Host: s.Host(), Capability: CapPushHooks}
	}
	sig, ok := s.driver.(Signatures)
	if !ok {
		return Hook{}, &UnsupportedError{Host: s.Host(), Capability: CapSignatures}
	}
	if err := s.check(key); err != nil {
		return Hook{}, err
	}
	if secret.Empty() {
		return Hook{}, fmt.Errorf("forge: refusing to register a hook for %q with no secret", key)
	}
	if callback == nil {
		return Hook{}, fmt.Errorf("forge: refusing to register a hook for %q with no callback", key)
	}
	if callback.Scheme != "https" && !s.reg.policy.AllowInsecureCallback {
		return Hook{}, fmt.Errorf("forge: callback %q is not https; a delivery carries a secret, set Policy.AllowInsecureCallback to accept it", callback.Redacted())
	}
	scheme := sig.Scheme()
	wire, err := scheme.Wire(secret)
	if err != nil {
		return Hook{}, err
	}
	handle, err := h.RegisterPushHook(ctx, key, callback, wire)
	if err != nil {
		return Hook{}, err
	}
	if handle == "" {
		return Hook{}, fmt.Errorf("forge: driver for %q returned an empty hook handle; nothing could ever remove that hook", s.Host())
	}
	return Hook{Key: key, Handle: handle, Scheme: scheme, Secret: secret}, nil
}

func (s *Session) ListPushHooks(ctx context.Context, key repokey.Key) ([]InstalledPushHook, error) {
	p, ok := s.client.(PushHooks)
	if !ok {
		return nil, &UnsupportedError{Host: s.Host(), Capability: CapPushHooks}
	}
	if err := s.check(key); err != nil {
		return nil, err
	}
	return p.ListPushHooks(ctx, key)
}

func (s *Session) RemovePushHook(ctx context.Context, key repokey.Key, h Handle) error {
	p, ok := s.client.(PushHooks)
	if !ok {
		return &UnsupportedError{Host: s.Host(), Capability: CapPushHooks}
	}
	if err := s.check(key); err != nil {
		return err
	}
	return p.RemovePushHook(ctx, key, h)
}

func (s *Session) CloneAddress(key repokey.Key, over Protocol) (Address, error) {
	c, ok := s.client.(CloneAddresses)
	if !ok {
		return Address{}, &UnsupportedError{Host: s.Host(), Capability: CapCloneAddr}
	}
	if err := s.check(key); err != nil {
		return Address{}, err
	}
	return c.CloneAddress(key, over)
}

// CanPush is git's question and not the forge's, and it is a separate method
// from Access for the reason PushProber's own doc gives: the credential it
// proves things about is a machine key, not a person. Reading a true from here
// as "this user may write" grants project ownership on the strength of a key
// tau itself issued.
func (s *Session) CanPush(ctx context.Context, key repokey.Key, sshPrivateKey []byte) (bool, error) {
	p, ok := s.client.(PushProber)
	if !ok {
		return false, &UnsupportedError{Host: s.Host(), Capability: CapPushProbe}
	}
	if err := s.check(key); err != nil {
		return false, err
	}
	return p.CanPush(ctx, key, sshPrivateKey)
}
