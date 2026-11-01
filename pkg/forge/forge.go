// Package forge is what tau asks a git host that git itself cannot answer.
//
// The host is the provider: a Registry maps a repokey host to a driver, so
// adding a forge is registering a value, never a provider enum or a switch.
//
// A Driver must say which keys it can address; a Client must answer who its
// credential is and what it may do to a repository. Everything else is an
// optional face, reached through a Session method that returns
// *UnsupportedError, never a zero value.
//
// A driver never verifies a signature: verification is a closed set of named
// Schemes implemented here, and a driver names one and reads a payload after
// the core verified it (see webhook.go). "No" and "could not ask" differ: a
// definitive negative is a value, everything else a typed error (see
// errors.go). Nothing from a forge SDK crosses this boundary; the one
// forge-assigned value is an opaque Handle. The package imports only
// pkg/git/repokey outside the standard library.
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

// Capability names one question, so a refusal can say which went unanswered.
// It never selects a driver or appears in config.
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

// Set is computed from type assertions, never declared by a driver, so a driver
// can neither over- nor under-claim.
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

// A face whose method signature is one character wrong compiles clean and drops
// out of the set; the suite compares sets to catch it.
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

// Driver is one forge's software, constructed for the host it serves so New can
// refuse a binding aiming a credential at another server. A deployment
// credential is baked in and reachable only through the driver.
type Driver interface {
	// Host is the repokey.Key.Host spelling this driver answers for:
	// lowercased, a port only when it is not the transport's default.
	Host() string

	// Addressable reports why this driver cannot address key, or nil. A driver
	// refuses a depth it cannot address and never flattens one; the registry calls
	// this once, before any request.
	Addressable(key repokey.Key) error

	// Bind returns a client acting as cred and ONLY as cred. It must refuse a
	// zero Credential with ErrNoCredential.
	Bind(cred Credential) (Client, error)
}

// Client is a driver acting as one credential, and stateless: every method takes
// the key it acts on. Consumers hold a *Session, which owns every refusal.
type Client interface {
	// Host is the host this client acts on, and it must be its driver's. The
	// registry checks it at bind time: a client answering for another host is
	// a credential aimed at the wrong server, one layer below the binding.
	Host() string

	// Whoami says who this credential is. It doubles as the only honest
	// token-validity check a forge offers.
	Whoami(ctx context.Context) (Identity, error)

	// Access is the authorization question. (AccessNone, nil) means the forge said
	// no; anything else is an error, so an outage never reads as a refusal.
	// Visibility is not permission: a public repository with no grant is
	// AccessRead.
	Access(ctx context.Context, key repokey.Key) (Access, error)

	// Repository is what only the forge knows. Key must be DERIVED from the
	// clone URL the forge returned, not echoed back from the argument.
	Repository(ctx context.Context, key repokey.Key) (Repository, error)
}

// Lister yields the repositories this credential can reach, with no options:
// affiliation vocabularies are per forge. Everything yielded is readable, and an
// absence proves nothing. yield returning false stops it without error.
type Lister interface {
	ListRepositories(ctx context.Context, yield func(Repository) bool) error
}

// Creator creates a repository at the key it will be addressed by, namespace
// included; a driver that cannot create in a namespace refuses the key.
type Creator interface {
	Create(ctx context.Context, key repokey.Key, opts CreateOptions) (Repository, error)
}

// CreateOptions is what the forges agree on.
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

type Deleter interface {
	Delete(ctx context.Context, key repokey.Key) error
}

// DeployKeys installs and removes machine clone credentials. Every key is
// read-only, with no parameter to change it: a builder only clones, and a write
// key nothing removes is a standing credential. publicKey is the
// authorized_keys public half.
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

// PushHooks registers and removes push webhooks. The driver never sees the key
// material, only wire, the encoding Scheme.Wire derives, so it cannot weaken,
// keep, log or re-encode a secret. Reached only through
// Session.RegisterPushHook.
type PushHooks interface {
	RegisterPushHook(ctx context.Context, key repokey.Key, callback *url.URL, wire string) (Handle, error)
	RemovePushHook(ctx context.Context, key repokey.Key, h Handle) error

	// ListPushHooks is every webhook on the repository, whoever registered it,
	// and only what a forge says about one without its secret: the handle and
	// where it delivers. It is how a consumer finds a hook IT registered
	// earlier, by its callback.
	ListPushHooks(ctx context.Context, key repokey.Key) ([]InstalledPushHook, error)
}

type InstalledPushHook struct {
	Handle   Handle
	Callback string
}

// CloneAddresses answers what address a clone of a key uses on this host, and as
// which account.
type CloneAddresses interface {
	CloneAddress(key repokey.Key, over Protocol) (Address, error)
}

// PushProber answers a different question from Access: whether the holder of a
// git credential, possibly a machine key, may push, proven by a receive-pack
// advertisement. A consumer substituting it for Access must write that down.
type PushProber interface {
	CanPush(ctx context.Context, key repokey.Key, sshPrivateKey []byte) (bool, error)
}

// Memberships answers whether a named human is an active member of a namespace.
// It hangs off the driver: a caller's token cannot answer it, so it uses a
// credential the deployment holds, which no session can reach. (false, nil) is
// a definitive non-member; everything else is an error.
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

	// Push reads a delivery the core has verified: VerifiedBody cannot be built
	// outside this package, so no unchecked bytes reach a parser. It returns a
	// claim the core checks against the hook's repository, or ErrNotAPush.
	Push(body VerifiedBody, header http.Header) (PushClaim, error)
}

// Spellings is how a forge says several of its addresses name one repository,
// as when https sits under a path prefix and ssh on another port; repokey stays
// free of any forge's vocabulary. Consulted through Registry.Key.
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

// Binding is how a forge is added: a value handed to New.
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

// Registry maps a host to its driver and is immutable after construction, so no
// lookup races a rebinding.
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

// Key turns a clone address into a repository key: repokey.Parse, then the
// canonical key of a driver serving or spelling that host. Any other URL, and
// any on a nil Registry, keeps repokey's parse. A canonical key on another host
// is refused.
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

func (r *Registry) Hosts() []string {
	out := make([]string, 0, len(r.drivers))
	for h := range r.drivers {
		out = append(out, h)
	}
	slices.Sort(out)
	return out
}

// For resolves the driver for a key's host, checks it can address the key, and
// binds a credential, once, before any request.
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
	// A client answering for another host is a credential aimed at the wrong
	// server, checked here because this is the only place a Client is built.
	if ch := c.Host(); ch != d.Host() {
		return nil, &CredentialLeakError{Bound: d.Host(), To: ch}
	}
	return &Session{reg: r, driver: d, client: c, anonymous: cred.IsAnonymous()}, nil
}

// IsActiveMember returns *UnsupportedError, never false, for a driver with no
// membership face. It hangs off the Registry: the deployment's credential
// answers it, which no session may reach.
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

// Session is a forge seen through one credential, concrete so every refusal is
// written once here. Capability belongs to (forge, credential), which is why
// membership is not reachable from here.
type Session struct {
	reg       *Registry
	driver    Driver
	client    Client
	anonymous bool
}

func (s *Session) Host() string      { return s.driver.Host() }
func (s *Session) IsAnonymous() bool { return s.anonymous }

// Capabilities is computed from type assertions on the bound client and its
// driver.
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

// Require checks a whole flow's capabilities before any side effect lands.
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

// RegisterPushHook is the only way a hook is created: the wire form is derived
// from the driver's own Scheme() and the Hook is stamped with that scheme, so
// the two are one value. The driver receives wire, never the Secret.
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

// CanPush is git's question: the credential is a machine key, not a person, so
// a true is not "this user may write".
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
