package fake

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
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

	// Moved is a repository that was RENAMED. This forge still answers for it
	// and answers WITH MovedTo, the way every real forge keeps an old path
	// resolving after a rename. It is the only fixture on which echoing the
	// argument back and deriving the key from the answer produce different
	// values, which makes it the only one that can assert the difference.
	Moved   repokey.Key
	MovedTo repokey.Key
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

	// scheme is how its https clone addresses are spelled: "https", or
	// "http" for a forge Serve put on loopback.
	scheme string
	git    gitStores

	// sshHost is where Serve answers git over ssh; empty when not served.
	sshHost string

	mu       sync.Mutex
	repos    map[string]*repo
	aliases  map[string]string
	hooks    map[forge.Handle]*hookRec
	deploy   map[forge.Handle]*keyRec
	seq      int
	lastWire string
	lastRO   bool
	requests []Request
	broken   bool
	redirect string

	// redirectStatus is what the armed answer carries. It is a fixture's
	// choice in the interface and the SUITE'S choice here, because it decides
	// which half of the defect is reached: 301, 302 and 303 make Go rewrite the
	// request to a GET, so a write following one lands nowhere and reads
	// somebody else's 200; 307 and 308 replay method and body, so it lands.
	redirectStatus int

	// issued is every address this forge has told the world about, and which
	// repository each one is for. A same-host redirect names an address, and
	// this is what turns that address back into the repository it addresses —
	// so a hop is followed to a REPOSITORY rather than to a string.
	issued map[string]repokey.Key

	// followed is the repository the current call was redirected to, set for
	// exactly one operation. It is what makes a followed hop act on somebody
	// else's repository, which is the whole of the defect being modelled.
	followed repokey.Key

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
		shape:   sh,
		repos:   make(map[string]*repo),
		aliases: make(map[string]string),
		hooks:   make(map[forge.Handle]*hookRec),
		deploy:  make(map[forge.Handle]*keyRec),
		issued:  make(map[string]repokey.Key),
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

	// acme/billing was renamed to acme/pay and this forge still answers for the
	// old name — with the new repository, whose clone addresses say so. Asking
	// about it is the only question whose answer differs between a driver that
	// derives the key and one that echoes the argument.
	c.keys.Moved, c.keys.MovedTo = c.rename("acme/billing", c.keys.Writable), c.keys.Writable
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

// RedirectOnce makes the next call answer with status, pointing at loc.
//
// THIS FORGE SENDS NOTHING, so following one is modelled rather than performed:
// a correct driver refuses the hop and this forge says so with an error naming
// the status, and the following faults instead act as an http.Client would.
// Either way the destination ends up somewhere the conformance suite can read
// it, which is the whole point: where a request WENT is not something the
// receiving end can report.
func (f *Forge) RedirectOnce(loc string, status int) {
	f.c.mu.Lock()
	f.c.redirect = loc
	f.c.redirectStatus = status
	f.c.mu.Unlock()
}

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

// identityPath is where this forge answers "who is this credential", and it is
// named because one fault sends that one request somewhere else.
const identityPath = "/api/user"

// record writes down what this forge would have sent, and remembers which
// repository the address is for. The credential goes in an Authorization
// header, which is where a driver must put it; FaultLeaksTokenInURL puts it
// where it must not.
func (c *core) record(method, path, token string, about repokey.Key) {
	host := c.shape.Host
	if path == identityPath && c.shape.faulty(FaultAsksAnotherHostWhoThisIs) {
		host = "elsewhere.invalid"
	}
	url := "https://" + host + path
	if c.shape.faulty(FaultLeaksTokenInURL) && token != "" {
		url += "?access_token=" + token
	}
	if about.Name != "" {
		c.issued[url] = about
	}
	hdr := make(http.Header)
	if token != "" {
		hdr.Set("Authorization", "Bearer "+token)
	}
	c.requests = append(c.requests, Request{Method: method, URL: url, Header: hdr})
}

// call is a request about no repository in particular — an identity, a
// membership. Everything about a repository goes through callAbout, so that an
// address this forge issued can be traced back to what it addresses.
func (c *core) call(method, path, token string) (bool, error) {
	return c.callAbout(method, path, token, repokey.Key{})
}

// callAbout is every request this forge serves: it records, then says whether
// the operation may GO ON.
//
// THE BOOL IS NOT A DUPLICATE OF THE ERROR. It is false with a nil error in
// exactly one situation, and that situation is a defect this suite exists to
// name: the driver followed a hop, something that is not this forge answered
// it, and the driver took that answer. A 204 from another host parses as
// nothing, so the operation reports success having done nothing at all — which
// is a `nil` error and no state change, and there is no error value that says
// so.
func (c *core) callAbout(method, path, token string, about repokey.Key) (bool, error) {
	c.mu.Lock()
	// One operation, one hop: whatever the last call was redirected to has no
	// business surviving into this one.
	c.followed = repokey.Key{}
	c.record(method, path, token, about)
	loc := c.redirect
	status := c.redirectStatus
	c.redirect, c.redirectStatus = "", 0
	broken := c.broken
	c.mu.Unlock()

	if loc != "" {
		return c.answerRedirect(method, loc, status, token)
	}
	if broken {
		if c.shape.faulty(FaultLeaksTokenInError) && token != "" {
			// The same mistake as the branch below, on the path a consumer
			// pastes most: an outage, quoted with the credential that was in
			// flight when it happened.
			return false, fmt.Errorf("fake: while acting as %s: %w", token, errUnreachable)
		}
		return false, errUnreachable
	}
	if token == badToken && !c.shape.faulty(FaultARefusedCredentialIsAccepted) {
		reason := "token was revoked"
		if c.shape.faulty(FaultLeaksTokenInError) {
			reason = "token " + token + " was revoked"
		}
		return false, &forge.CredentialError{Host: c.shape.Host, Reason: reason}
	}
	return true, nil
}

// hopper is the client the one sending fault uses. Its transport is bare, so
// nothing about the machine this runs on — a proxy in the environment, most of
// all — can change where the hop goes.
var hopper = &http.Client{Transport: &http.Transport{}}

// answerRedirect is what this forge does once RedirectOnce armed it: it answers
// somewhere else, and what happens next is the driver's choice. A correct one
// refuses the hop, and this says so with an error.
//
// FaultFollowsRedirect REALLY SENDS, and it is the only thing in this package
// that sends anything. It has to: the whole point of that fault is a request
// arriving somewhere it was not meant to, and an arrival nobody can observe is
// the defect being modelled rather than a test of it. The credential goes with
// it, because a header attached by a RoundTripper is attached again on the next
// hop — which is exactly how a driver that dropped its pinned transport leaks
// one. loc is whatever armed this, in practice a loopback server the caller
// started to watch.
func (c *core) answerRedirect(method, loc string, status int, token string) (bool, error) {
	if c.onThisHost(loc) {
		if !c.shape.faulty(FaultFollowsSameHostRedirect) {
			return false, fmt.Errorf("fake: %s answered %d to %s, which is another repository on this host; the hop was refused", c.shape.Host, status, loc)
		}
		return c.followSameHost(method, loc, status, token)
	}
	// The deployment's credential rides a client of its own, and a driver can
	// hold a redirect policy on one client and not the other.
	onDeployment := c.shape.faulty(FaultFollowsRedirectOnTheDeploymentCredential) && token == deployToken
	if !c.shape.faulty(FaultFollowsRedirect) && !onDeployment {
		return false, fmt.Errorf("fake: %s answered %d to %s, which is not this host; the hop was refused", c.shape.Host, status, loc)
	}

	req, err := http.NewRequest(method, loc, nil)
	if err != nil {
		return false, fmt.Errorf("fake: the redirect names %q, which is not a request: %w", loc, err)
	}
	hdr := make(http.Header)
	if token != "" {
		hdr.Set("Authorization", "Bearer "+token)
	}
	req.Header = hdr.Clone()
	if resp, err := hopper.Do(req); err == nil {
		resp.Body.Close()
	}

	// And written down as well, so a Subject reading Requests sees the hop too.
	c.mu.Lock()
	c.requests = append(c.requests, Request{Method: method, URL: loc, Header: hdr})
	c.mu.Unlock()

	// AND THE ANSWER FROM THERE IS THE ANSWER. This is what a driver that
	// follows a redirect actually does: the hop returns 204, which parses as
	// nothing, and the operation reports success having done nothing. Returning
	// an error here instead would model a driver that followed the hop and then
	// happened to fail — which is the milder half, and the half that leaves "an
	// answer from another host was taken for the forge's" unassertable.
	return false, nil
}

// onThisHost is whether a redirect names this forge's own host. It decides which
// of the two redirect defects is in play, and it is also what keeps this package
// off the network: the sending fault below dials loc, and loc must therefore be
// the loopback server a caller started, never a host name this forge invented.
func (c *core) onThisHost(loc string) bool {
	u, err := url.Parse(loc)
	return err == nil && u.Host == c.shape.Host
}

// followSameHost is the hop a credential pin permits: same host, credential
// attached again, and a repository at the other end that the caller never named.
//
// NOTHING IS SENT, because there is nothing to send to — this forge is a map.
// What a real driver's http.Client would do is done here directly: the request
// is written down as having gone to loc with the credential on it, and the
// operation now acts on whatever repository loc addresses. Everything the
// operation returns still describes the repository the CALLER named, because
// that is the argument it was given, and that is precisely what makes the defect
// invisible from above.
func (c *core) followSameHost(method, loc string, status int, token string) (bool, error) {
	hdr := make(http.Header)
	if token != "" {
		hdr.Set("Authorization", "Bearer "+token)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, Request{Method: method, URL: loc, Header: hdr})

	// THE STATUS DECIDES WHETHER ANYTHING IS WRITTEN. Go rewrites a non-GET to
	// a GET on 301, 302 and 303, so a write following one never lands: the
	// driver reads the other repository's answer and reports success having
	// done nothing. 307 and 308 replay method and body, so it lands there.
	if rewritesToGET(status) && method != http.MethodGet {
		return false, nil
	}
	// An address this forge never issued names no repository it can serve, so
	// the operation goes on against the one it was asked about — the hop was
	// followed and landed nowhere interesting.
	c.followed = c.issued[loc]
	// An armed redirect is answered before the outage and before the credential,
	// which is the order call itself uses: a forge redirects on the address it
	// was given, and has read neither by then.
	return true, nil
}

// rewritesToGET is the stdlib's rule, which is HTTP's: only 307 and 308 promise
// to replay the method.
func rewritesToGET(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther:
		return true
	}
	return false
}

// rename makes this forge answer for `from` with the repository `to` names, and
// returns the old key. It is a redirect and not a copy: there is one repository
// and it has one identity, which is the whole point.
func (c *core) rename(from string, to repokey.Key) repokey.Key {
	old := c.parse(from)
	c.aliases[old.String()] = to.String()
	return old
}

func (c *core) lookup(k repokey.Key) (*repo, bool) {
	// A followed hop resolves to the repository the redirect named, and the
	// caller's key is never consulted again. Every face reaches its repository
	// through here, so this is the one place the substitution has to live.
	if c.followed.Name != "" {
		k = c.followed
	}
	name := k.String()
	if to, moved := c.aliases[name]; moved {
		name = to
	}
	r, ok := c.repos[name]
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
	if r.grant == forge.AccessWrite && c.shape.faulty(FaultWritableReadsAsReadOnly) {
		return forge.AccessRead
	}
	if r.grant == forge.AccessNone && c.shape.faulty(FaultAHiddenRepositoryIsReadable) {
		return forge.AccessRead
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
	if over == forge.SSH && c.sshHost != "" {
		return "ssh://git@" + c.sshHost + "/" + k.FullName() + ".git"
	}
	if over == forge.SSH {
		return "ssh://git@" + c.shape.Host + "/" + k.FullName() + ".git"
	}
	scheme := c.scheme
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + c.shape.Host + "/" + k.FullName() + ".git"
}

// servedCloneURL is a clone address this forge REPORTS, under whichever fault
// governs the surface asking for it: the record a repository answer carries, or
// the address the CloneAddress face hands a builder. Under that fault it names a
// different repository on this host — a legal address, well formed, and not the
// one it is supposed to be about.
//
// It is deliberately not cloneURL itself: a delivery's payload also carries a
// clone address, and a payload naming another repository is the confused-deputy
// defect, which has its own fixture and its own assertion. One fault, one
// mistake.
func (c *core) servedCloneURL(k repokey.Key, over forge.Protocol, under Fault) string {
	if c.shape.faulty(under) {
		k = c.parse("acme/elsewhere")
	}
	return c.cloneURL(k, over)
}

func (c *core) repository(r *repo, asked repokey.Key) forge.Repository {
	https := c.servedCloneURL(r.key, forge.HTTPS, FaultRepositoryAddressNamesAnother)
	ssh := c.servedCloneURL(r.key, forge.SSH, FaultRepositoryAddressNamesAnother)
	if c.shape.faulty(FaultTheRecordsAddressCarriesTheCredential) {
		https = c.credentialed(https)
		ssh = c.credentialed(ssh)
	}
	branch := r.defaultBranch
	if c.shape.faulty(FaultTheRecordHasNoDefaultBranch) {
		branch = ""
	}
	key := r.key
	if c.shape.faulty(FaultTheRecordNamesAnotherRepository) {
		key = c.parse("acme/elsewhere")
	}
	if c.shape.faulty(FaultEchoesKey) {
		// ONE defect and only one: the argument is echoed back, and the clone
		// addresses are left saying what the forge actually served. On every
		// fixture but Moved the two are the same repository, so this is
		// invisible — which is precisely what makes Moved the fixture the
		// derivation contract is asserted on.
		key = asked
	}
	return forge.Repository{
		Key:           key,
		HTTPS:         https,
		SSH:           ssh,
		DefaultBranch: branch,
		Private:       r.private,
	}
}

// credentialed splices the credential into a clone address, which is what puts
// one in .git/config, in process arguments and in every error that quotes a
// remote. repokey refuses a URL carrying one, so this is visible to anything
// that re-derives a key from an address — and invisible to everything else.
func (c *core) credentialed(spelling string) string {
	for _, scheme := range []string{"https://", "ssh://"} {
		if strings.HasPrefix(spelling, scheme) {
			return scheme + "x-access-token:" + goodToken + "@" + strings.TrimPrefix(spelling, scheme)
		}
	}
	return spelling
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
	if !strings.Contains(k.Owner, "/") {
		return nil
	}
	if c.shape.Nested {
		if c.shape.faulty(FaultANestingForgeRefusesADepth) {
			return errors.New("this forge cannot address a nested owner")
		}
		return nil
	}
	if c.shape.faulty(FaultFlattensDeepKey) {
		return nil
	}
	return errors.New("this forge addresses exactly one owner and one name")
}
