package fake

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/v2/forge"
)

// Request is what this forge records instead of sending. A real driver's tests
// record the http.Requests an httptest server saw; the fields are the ones the
// conformance suite's credential canary reads, so the two are interchangeable
// to it.
type Request struct {
	Method string
	URL    string
	Header http.Header
}

// Keys are the fixtures every shape seeds, named by what the conformance suite
// asks of them. They exist here rather than in a test so that a consumer's
// tests and the suite's tests name the same repositories.
type Keys struct {
	Writable repokey.Key // >= AccessWrite for Token
	ReadOnly repokey.Key // public, no grant: EXACTLY AccessRead
	NoAccess repokey.Key // exists, the credential holds nothing
	Missing  repokey.Key // does not exist
	Admin    repokey.Key // AccessAdmin; hooks, deploy keys and the probe run here
	Deep     repokey.Key // Owner contains "/"; addressable only when Nested

	// Deletable is AccessAdmin and nothing else touches it, so deleting it
	// cannot pull the ground out from under another assertion. Creatable does
	// not exist yet; Uncreatable is in a namespace this credential may not
	// write.
	Deletable   repokey.Key
	Creatable   repokey.Key
	Uncreatable repokey.Key
}

// Membership fixtures.
const (
	Namespace = "acme"
	Member    = "alice"
	NonMember = "mallory"
)

// The token this forge honours, and one it refuses. They are long enough and
// odd enough that finding either in a URL or an error string is unambiguous.
const (
	goodToken = "fake-token-6f1c8a2e"
	badToken  = "fake-token-revoked-b0b0"
)

// errUnreachable is what Break makes every call that talks to the forge return.
// It is a plain error on purpose: an outage is not a member of the taxonomy,
// and a consumer must not be able to pattern-match it into a verdict.
var errUnreachable = errors.New("fake: forge is unreachable")

// Forge is one in-memory forge plus the handles a test needs on it. The driver
// it hands out is a different value, because which optional faces a driver has
// is decided by its Go type and cannot be decided at runtime.
type Forge struct{ c *core }

type core struct {
	shape Shape

	mu       sync.Mutex
	repos    map[string]*repo
	hooks    map[forge.Handle]*hookRec
	deploy   map[forge.Handle]*keyRec
	seq      int
	lastWire string
	lastRO   bool
	requests []Request
	broken   bool

	keys Keys
}

type repo struct {
	key           repokey.Key
	private       bool
	defaultBranch string
	grant         forge.Access // what goodToken holds on it
}

type hookRec struct {
	key      repokey.Key
	callback string
	wire     string
}

type keyRec struct {
	key      repokey.Key
	title    string
	pub      string
	readOnly bool
}

// New builds a forge of the given shape, seeded with the fixtures in Keys.
func New(sh Shape) *Forge {
	if sh.Host == "" {
		panic("fake: a shape names no host")
	}
	if !sh.omits(forge.CapSignatures) && sh.Scheme == "" {
		panic("fake: a shape that signs names no scheme")
	}
	c := &core{
		shape:  sh,
		repos:  make(map[string]*repo),
		hooks:  make(map[forge.Handle]*hookRec),
		deploy: make(map[forge.Handle]*keyRec),
	}
	c.keys = Keys{
		Writable: c.seed("acme/pay", true, forge.AccessWrite),
		ReadOnly: c.seed("acme/public", false, forge.AccessRead),
		NoAccess: c.seed("stranger/secret", true, forge.AccessNone),
		Missing:  c.parse("acme/nope"),
		Admin:    c.seed("acme/site", true, forge.AccessAdmin),

		Deletable:   c.seed("acme/scratch", true, forge.AccessAdmin),
		Creatable:   c.parse("acme/fresh"),
		Uncreatable: c.parse("stranger/fresh"),
	}
	// The deep fixture is seeded on every shape, so that a forge which cannot
	// address one is refusing a repository that genuinely exists rather than
	// one that happens not to.
	c.keys.Deep = c.seed("acme/team/widget", true, forge.AccessAdmin)
	return &Forge{c: c}
}

func (c *core) parse(full string) repokey.Key {
	k, err := repokey.Parse("https://" + c.shape.Host + "/" + full + ".git")
	if err != nil {
		panic("fake: fixture " + full + ": " + err.Error())
	}
	return k
}

func (c *core) seed(full string, private bool, grant forge.Access) repokey.Key {
	k := c.parse(full)
	c.repos[k.String()] = &repo{key: k, private: private, defaultBranch: "main", grant: grant}
	return k
}

// Driver is the forge.Driver for this shape. Its Go type carries exactly the
// optional faces the shape did not omit.
func (f *Forge) Driver() forge.Driver { return newDriver(f.c) }

// Registry is the convenience a consumer's test wants: one binding, one
// registry. It fails the same way New does, so a shape whose scheme the policy
// refuses is refused here too.
func (f *Forge) Registry(policy forge.Policy) (*forge.Registry, error) {
	return forge.New(policy, forge.Binding{Host: f.c.shape.Host, Driver: f.Driver()})
}

func (f *Forge) Host() string { return f.c.shape.Host }
func (f *Forge) Keys() Keys   { return f.c.keys }

// Token is the credential this forge honours; BadToken is one it refuses with a
// *forge.CredentialError, which is a statement about the credential and never
// about the repository.
func (f *Forge) Token() forge.Credential    { return forge.Token(goodToken) }
func (f *Forge) BadToken() forge.Credential { return forge.Token(badToken) }

// TokenString and BadTokenString are the raw values, for a test that needs to
// look for one where it must never appear.
func (f *Forge) TokenString() string    { return goodToken }
func (f *Forge) BadTokenString() string { return badToken }

// Requests reports what this forge has been asked to do since the last call,
// and resets. It is what proves a refusal happened BEFORE the wire.
func (f *Forge) Requests() []Request {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	out := f.c.requests
	f.c.requests = nil
	return out
}

// Break makes every call that talks to the forge fail at the transport;
// Unbreak restores. It is the only way to assert the invariant that matters
// most: an outage returns an error and never a definitive no.
func (f *Forge) Break()   { f.c.mu.Lock(); f.c.broken = true; f.c.mu.Unlock() }
func (f *Forge) Unbreak() { f.c.mu.Lock(); f.c.broken = false; f.c.mu.Unlock() }

// LastDeployKeyReadOnly reports whether the last installed key was read-only.
// It is a probe the FORGE supplies, so a driver that lies about it passes — the
// interface has no parameter to inspect. Saying so is more honest than a test
// that pretends otherwise.
func (f *Forge) LastDeployKeyReadOnly() bool {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	return f.c.lastRO
}

func (f *Forge) DeployKeyInstalled(h forge.Handle) bool {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	_, ok := f.c.deploy[h]
	return ok
}

// LastHookWire is the string the forge was actually given, which is what proves
// a driver passed it through unmangled instead of re-encoding it into something
// no delivery will ever match.
func (f *Forge) LastHookWire() string {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	return f.c.lastWire
}

func (f *Forge) HookInstalled(h forge.Handle) bool {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	_, ok := f.c.hooks[h]
	return ok
}

// PushKey and NoPushKey are the two ssh private keys the push probe
// distinguishes. They are opaque bytes: nothing here does crypto, because the
// question the probe answers is a git-protocol one and the fake is not git.
func (f *Forge) PushKey() []byte   { return []byte("fake-ssh-key-may-push") }
func (f *Forge) NoPushKey() []byte { return []byte("fake-ssh-key-read-only") }

// ─────────────────────────── the shared implementation ───────────────────────────

// record writes down what this forge would have sent. The credential goes in an
// Authorization header, which is where a driver must put it; FaultLeaksTokenInURL
// puts it where it must not.
func (c *core) record(method, path, token string) {
	url := "https://" + c.shape.Host + path
	if c.shape.faulty(FaultLeaksTokenInURL) && token != "" {
		url += "?access_token=" + token
	}
	hdr := make(http.Header)
	if token != "" {
		hdr.Set("Authorization", "Bearer "+token)
	}
	c.requests = append(c.requests, Request{Method: method, URL: url, Header: hdr})
}

// call is every request this forge serves: it records, then fails if broken or
// if the credential is refused.
func (c *core) call(method, path, token string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.record(method, path, token)
	if c.broken {
		return errUnreachable
	}
	if token == badToken {
		reason := "token was revoked"
		if c.shape.faulty(FaultLeaksTokenInError) {
			reason = "token " + token + " was revoked"
		}
		return &forge.CredentialError{Host: c.shape.Host, Reason: reason}
	}
	return nil
}

func (c *core) lookup(k repokey.Key) (*repo, bool) {
	r, ok := c.repos[k.String()]
	return r, ok
}

// grant is what a token holds on a repository. A public repository is readable
// by everyone and that is ALL it is: the fault below is the whole reason the
// rung exists.
func (c *core) grant(r *repo, token string) forge.Access {
	if token != goodToken {
		// Nobody in particular. A public repository still reads; a private one
		// is not merely refused, it is not there.
		if r.private {
			return forge.AccessNone
		}
		return forge.AccessRead
	}
	if !r.private && c.shape.faulty(FaultVisibilityIsPermission) {
		return forge.AccessWrite
	}
	return r.grant
}

// flatten is what a driver does WRONG when it meets a depth it cannot address:
// it joins the segments into a name no repository has, and the request lands
// somewhere other than where the caller pointed it. Only FaultFlattensDeepKey
// reaches it.
func (c *core) flatten(k repokey.Key) repokey.Key {
	if !c.shape.faulty(FaultFlattensDeepKey) || c.shape.Nested {
		return k
	}
	k.Owner = strings.ReplaceAll(k.Owner, "/", "-")
	return k
}

func (c *core) cloneURL(k repokey.Key, over forge.Protocol) string {
	if over == forge.SSH {
		return "ssh://git@" + c.shape.Host + "/" + k.FullName() + ".git"
	}
	return "https://" + c.shape.Host + "/" + k.FullName() + ".git"
}

func (c *core) repository(r *repo, asked repokey.Key) forge.Repository {
	https := c.cloneURL(r.key, forge.HTTPS)
	ssh := c.cloneURL(r.key, forge.SSH)
	key := r.key
	if c.shape.faulty(FaultEchoesKey) {
		// The argument is echoed back while the addresses point at the
		// repository the forge actually served, so the two disagree and only a
		// re-parse notices.
		key = asked
		https = c.cloneURL(c.keys.ReadOnly, forge.HTTPS)
		ssh = c.cloneURL(c.keys.ReadOnly, forge.SSH)
	}
	return forge.Repository{
		Key:           key,
		HTTPS:         https,
		SSH:           ssh,
		DefaultBranch: r.defaultBranch,
		Private:       r.private,
	}
}

func (c *core) nextHandle(kind string) forge.Handle {
	c.seq++
	if c.shape.Handles == HandlesUUID {
		return forge.Handle(fmt.Sprintf("{%08x-0000-4000-8000-%012x}", len(kind), c.seq))
	}
	return forge.Handle(strconv.Itoa(1000 + c.seq))
}

// addressable is Driver.Addressable's body, shared so the fault has one home.
func (c *core) addressable(k repokey.Key) error {
	if c.shape.Nested || !strings.Contains(k.Owner, "/") {
		return nil
	}
	if c.shape.faulty(FaultFlattensDeepKey) {
		return nil
	}
	return errors.New("this forge addresses exactly one owner and one name")
}
