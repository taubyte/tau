package forgetest

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/v2/forge"
)

// commitA is a whole 40-hex object id. Nothing here abbreviates one: an
// abbreviation that later matches two objects is a build of the wrong commit.
const commitA = "1f0c9d2b3a4e5f60718293a4b5c6d7e8f9012345"

// deletedOID is git's deletion sentinel.
const deletedOID = "0000000000000000000000000000000000000000"

// foreignHost is a host no subject is ever bound to.
const foreignHost = "forge.invalid"

// Run puts a driver through the whole suite. It never skips: a capability the
// driver does not have is asserted absent, which is a contract of its own.
func Run(t *testing.T, s Subject) {
	t.Helper()
	if s.Name == "" {
		run(realTB{t}, s)
		return
	}
	realTB{t}.Run(s.Name, func(t tb) { run(t, s) })
}

type runner struct {
	s      Subject
	driver forge.Driver
	ctx    context.Context

	// errs is every error the suite saw. One subtest at the end reads them all
	// looking for the credential, because a token that reaches a log reaches it
	// through an error string more often than through anything else.
	errs []error
}

func (r *runner) note(err error) error {
	if err != nil {
		r.errs = append(r.errs, err)
	}
	return err
}

func run(t tb, s Subject) {
	r := &runner{s: s, ctx: context.Background()}
	r.preflight(t)

	t.Run("host-is-a-repokey-spelling", r.hostSpelling)
	t.Run("addressable-refuses-a-depth-it-cannot-address", r.addressableDepth)
	t.Run("a-key-on-another-host-is-refused", r.foreignKey)
	t.Run("a-zero-credential-is-refused", r.zeroCredential)
	t.Run("an-anonymous-session-answers-no-authorization-question", r.anonymous)
	t.Run("capabilities-are-exactly-what-was-declared", r.capabilities)
	t.Run("whoami", r.whoami)
	t.Run("access", r.access)
	t.Run("repository-round-trips-the-key", r.repository)
	t.Run("the-key-is-derived-from-the-forges-answer", r.derivedKey)
	t.Run("an-outage-is-not-a-refusal", r.outage)

	t.Run("list", r.list)
	t.Run("create", r.create)
	t.Run("delete", r.delete)
	t.Run("deploy-keys", r.deployKeys)
	t.Run("push-hooks", r.pushHooks)
	t.Run("clone-address", r.cloneAddress)
	t.Run("push-probe", r.pushProbe)
	t.Run("membership", r.membership)

	t.Run("the-credential-never-travels", r.credentialNeverTravels)
}

// preflight refuses a Subject that cannot be run. Every one of these is a
// Fatalf and not a skip: a suite that quietly runs less than it says it does is
// worse than no suite.
func (r *runner) preflight(t tb) {
	t.Helper()
	s := r.s
	if s.Registry == nil || s.Host == "" {
		t.Fatalf("forgetest: Subject needs a Registry and a Host")
	}
	if s.Expect == nil {
		t.Fatalf("forgetest: Subject.Expect is the exact capability set and cannot be nil")
	}
	if s.Requests == nil || s.Break == nil || s.Unbreak == nil {
		t.Fatalf("forgetest: Subject needs Requests, Break and Unbreak; without them 'refused before the wire' and 'an outage is not a refusal' are unassertable")
	}
	if s.Token == "" {
		t.Fatalf("forgetest: Subject.Token is the raw credential the canary looks for and cannot be empty")
	}
	d, err := s.Registry.Driver(s.Host)
	if err != nil {
		t.Fatalf("forgetest: the Registry has no driver for %q: %v", s.Host, err)
	}
	if hosts := s.Registry.Hosts(); len(hosts) != 1 {
		t.Fatalf("forgetest: the Registry must hold exactly one binding, holds %v", hosts)
	}
	r.driver = d

	for name, k := range map[string]repokey.Key{
		"Writable": s.Writable, "ReadOnly": s.ReadOnly, "NoAccess": s.NoAccess,
		"Missing": s.Missing, "Admin": s.Admin, "Deletable": s.Deletable,
		"Creatable": s.Creatable, "Uncreatable": s.Uncreatable, "Deep": s.Deep,
		"Moved": s.Moved, "MovedTo": s.MovedTo,
	} {
		if k.Name == "" {
			t.Fatalf("forgetest: fixture %s is not set", name)
		}
		if k.Host != s.Host {
			t.Fatalf("forgetest: fixture %s is on host %q, not %q", name, k.Host, s.Host)
		}
	}
	if !strings.Contains(s.Deep.Owner, "/") {
		t.Fatalf("forgetest: fixture Deep must have a nested owner, has %q", s.Deep.Owner)
	}
	// A Subject whose Moved and MovedTo are one key has opted out of the
	// derivation assertion without saying so: echoing the argument back and
	// deriving the key from the answer produce the same value, which is exactly
	// the blind spot this fixture exists to close.
	if s.Moved == s.MovedTo {
		t.Fatalf("forgetest: fixtures Moved and MovedTo are both %q; the forge must answer for one key and answer WITH another, or a driver that echoes its argument is indistinguishable from one that derives", s.Moved)
	}

	if s.Expect.Has(forge.CapDeployKeys) && (s.LastDeployKeyReadOnly == nil || s.DeployKeyInstalled == nil) {
		t.Fatalf("forgetest: CapDeployKeys needs LastDeployKeyReadOnly and DeployKeyInstalled")
	}
	if s.Expect.Has(forge.CapPushHooks) && (s.LastHookWire == nil || s.HookInstalled == nil) {
		t.Fatalf("forgetest: CapPushHooks needs LastHookWire and HookInstalled")
	}
	if s.Expect.Has(forge.CapSignatures) {
		if s.Delivery == nil || s.DeliveryFor == nil || s.NonPushDelivery == nil {
			t.Fatalf("forgetest: CapSignatures needs Delivery, DeliveryFor and NonPushDelivery")
		}
		if r.scheme(t) == forge.SchemeStandardWebhooks && s.DeliveryAt == nil {
			t.Fatalf("forgetest: %s signs a timestamp, so DeliveryAt is needed for the replay-window cases", forge.SchemeStandardWebhooks)
		}
	}
	if s.Expect.Has(forge.CapMembership) && (s.Namespace == "" || s.Member == "" || s.NonMember == "") {
		t.Fatalf("forgetest: CapMembership needs Namespace, Member and NonMember")
	}
	if s.Expect.Has(forge.CapPushProbe) && (len(s.PushKey) == 0 || len(s.NoPushKey) == 0) {
		t.Fatalf("forgetest: CapPushProbe needs PushKey and NoPushKey")
	}
	s.Requests()
}

// scheme is the driver's own, or "" when it has no Signatures face.
func (r *runner) scheme(t tb) forge.Scheme {
	t.Helper()
	sig, ok := r.driver.(forge.Signatures)
	if !ok {
		return ""
	}
	return sig.Scheme()
}

// session binds Credential to a key. Every mandatory assertion starts here,
// because a consumer never holds anything else.
func (r *runner) session(t tb, k repokey.Key) *forge.Session {
	t.Helper()
	sess, err := r.s.Registry.For(k, r.s.Credential)
	require.NoError(t, err, "binding a session for %q", k)
	return sess
}

// ─────────────────────────── the mandatory contract ───────────────────────────

// hostSpelling catches "GitHub.com", a trailing slash and a scheme in the host —
// each of which yields a driver that is registered and that no key will ever
// resolve to.
func (r *runner) hostSpelling(t tb) {
	t.Helper()
	k, err := repokey.Parse("https://" + r.s.Host + "/owner/name")
	require.NoError(t, err, "Host() %q is not something a clone URL can be built from", r.s.Host)
	assert.Equal(t, r.s.Host, k.Host, "Host() is not the spelling repokey derives from a clone URL to it")
	assert.Equal(t, r.s.Host, r.driver.Host(), "the driver's own Host() disagrees with the Subject")
}

// addressableDepth is the anti-GitHub assertion: a forge that takes exactly one
// owner and one name must REFUSE a nested key, before any request, rather than
// join the segments into a name no repository has.
func (r *runner) addressableDepth(t tb) {
	t.Helper()
	require.NoError(t, r.driver.Addressable(r.s.Writable), "the driver cannot address its own Writable fixture")

	r.s.Requests()
	sess, err := r.s.Registry.For(r.s.Deep, r.s.Credential)
	if !r.s.DeepNests {
		var ua *forge.UnaddressableError
		require.ErrorAs(t, r.note(err), &ua, "a forge that does not nest must refuse %q", r.s.Deep)
		assert.Equal(t, r.s.Deep, ua.Key)
		assert.Empty(t, r.s.Requests(), "the depth was refused only after a request went out; it must be refused before the wire")
		return
	}

	require.NoError(t, err, "a nesting forge must address %q", r.s.Deep)
	lvl, err := sess.Access(r.ctx, r.s.Deep)
	require.NoError(t, r.note(err))
	assert.Equal(t, forge.AccessAdmin, lvl, "Deep must be AccessAdmin so the round trip can run on it")

	repo, err := sess.Repository(r.ctx, r.s.Deep)
	require.NoError(t, r.note(err))
	r.assertRepository(t, repo, r.s.Deep)
}

func (r *runner) foreignKey(t tb) {
	t.Helper()
	foreign, err := repokey.Parse("https://" + foreignHost + "/someone/thing")
	require.NoError(t, err)

	var nd *forge.NoDriverError
	_, err = r.s.Registry.For(foreign, r.s.Credential)
	require.ErrorAs(t, r.note(err), &nd, "a host with no driver must be *NoDriverError and never a default")
	assert.Equal(t, foreignHost, nd.Host)

	// And a session already bound cannot be walked onto another host's key.
	sess := r.session(t, r.s.Writable)
	var wh *forge.WrongHostError
	_, err = sess.Access(r.ctx, foreign)
	require.ErrorAs(t, r.note(err), &wh, "a session must refuse a key on another host")
}

func (r *runner) zeroCredential(t tb) {
	t.Helper()
	_, err := r.s.Registry.For(r.s.Writable, forge.Credential{})
	require.ErrorIs(t, r.note(err), forge.ErrNoCredential, "a zero credential must never degrade into anonymity")
	_, err = r.s.Registry.Host(r.s.Host, forge.Credential{})
	require.ErrorIs(t, r.note(err), forge.ErrNoCredential)
}

func (r *runner) anonymous(t tb) {
	t.Helper()
	sess, err := r.s.Registry.For(r.s.Writable, forge.Anonymous())
	require.NoError(t, err, "Anonymous() must bind; it is refused at the question, not at the door")
	assert.True(t, sess.IsAnonymous())

	lvl, err := sess.Access(r.ctx, r.s.Writable)
	require.ErrorIs(t, r.note(err), forge.ErrAnonymous)
	assert.Equal(t, forge.AccessNone, lvl, "a refused authorization question must still read as no access")

	_, err = sess.Whoami(r.ctx)
	require.ErrorIs(t, r.note(err), forge.ErrAnonymous)
}

// capabilities compares the COMPUTED set to the declared one for equality, not
// containment. A face whose method signature is one type wrong compiles, fails
// its type assertion in silence, and drops out of the set; only equality names
// which one went missing.
func (r *runner) capabilities(t tb) {
	t.Helper()
	sess := r.session(t, r.s.Writable)
	got := sess.Capabilities()
	assert.True(t, got.Equal(r.s.Expect),
		"capabilities = %q, want %q", got.String(), r.s.Expect.String())

	require.NoError(t, sess.Require(forge.CapIdentity, forge.CapAccess, forge.CapRepository),
		"the three mandatory capabilities are not optional")
	for _, c := range allCapabilities {
		err := sess.Require(c)
		if r.s.Expect.Has(c) {
			assert.NoError(t, err, "Require(%s) refused a capability the session has", c)
			continue
		}
		var un *forge.UnsupportedError
		if assert.ErrorAs(t, r.note(err), &un, "Require(%s) must refuse a capability the session lacks", c) {
			assert.Equal(t, c, un.Capability)
			assert.Equal(t, r.s.Host, un.Host)
		}
	}
}

var allCapabilities = []forge.Capability{
	forge.CapList, forge.CapCreate, forge.CapDelete, forge.CapDeployKeys,
	forge.CapPushHooks, forge.CapCloneAddr, forge.CapPushProbe,
	forge.CapMembership, forge.CapSignatures,
}

func (r *runner) whoami(t tb) {
	t.Helper()
	sess := r.session(t, r.s.Writable)
	who, err := sess.Whoami(r.ctx)
	require.NoError(t, r.note(err))
	assert.NotEmpty(t, who.Login, "Whoami answered with no login; it is the only honest token check there is")

	bad, err := r.s.Registry.For(r.s.Writable, r.s.BadCredential)
	require.NoError(t, err)
	who, err = bad.Whoami(r.ctx)
	require.Error(t, r.note(err), "a refused credential must be an error and never a zero Identity with a nil error")
	assert.Equal(t, forge.Identity{}, who, "a failed call returned a value beside its error")
}

// access is the centrepiece. Everything in this package exists so that these
// five lines can be true at once.
func (r *runner) access(t tb) {
	t.Helper()
	sess := r.session(t, r.s.Writable)

	lvl, err := sess.Access(r.ctx, r.s.Writable)
	require.NoError(t, r.note(err))
	assert.True(t, lvl.CanWrite(), "Writable read as %s", lvl)

	// VISIBILITY IS NOT PERMISSION.
	lvl, err = sess.Access(r.ctx, r.s.ReadOnly)
	require.NoError(t, r.note(err))
	assert.Equal(t, forge.AccessRead, lvl, "a readable repository with no grant read as %s", lvl)
	assert.False(t, lvl.CanWrite())

	// A repository that exists and grants nothing: either answer is honest, and
	// a zero Access with a nil error and no such repository is not.
	sessNo := r.session(t, r.s.NoAccess)
	lvl, err = sessNo.Access(r.ctx, r.s.NoAccess)
	if err != nil {
		var nf *forge.NotFoundError
		assert.ErrorAs(t, r.note(err), &nf, "a repository the credential holds nothing on must be AccessNone or *NotFoundError")
	} else {
		assert.Equal(t, forge.AccessNone, lvl, "a repository the credential holds nothing on read as %s", lvl)
	}

	sessMissing := r.session(t, r.s.Missing)
	lvl, err = sessMissing.Access(r.ctx, r.s.Missing)
	var nf *forge.NotFoundError
	require.ErrorAs(t, r.note(err), &nf, "a repository that does not exist must not answer an authorization question")
	assert.Equal(t, forge.AccessNone, lvl, "a failed Access returned a level a caller could act on")

	// A refused credential is a statement about the credential, not a verdict
	// on the repository.
	bad, err := r.s.Registry.For(r.s.Writable, r.s.BadCredential)
	require.NoError(t, err)
	lvl, err = bad.Access(r.ctx, r.s.Writable)
	require.Error(t, r.note(err), "a refused credential must not read as AccessNone")
	assert.Equal(t, forge.AccessNone, lvl)
}

func (r *runner) repository(t tb) {
	t.Helper()
	sess := r.session(t, r.s.Writable)
	repo, err := sess.Repository(r.ctx, r.s.Writable)
	require.NoError(t, r.note(err))
	r.assertRepository(t, repo, r.s.Writable)

	repo, err = sess.Repository(r.ctx, r.s.Missing)
	var nf *forge.NotFoundError
	require.ErrorAs(t, r.note(err), &nf)
	assert.Equal(t, forge.Repository{}, repo, "a failed Repository returned a value beside its error")
}

// derivedKey is the OTHER half of the round trip, and the half that was
// missing. Repository's contract is that Key is DERIVED from the clone URL the
// forge returned and never echoed back from the argument — and on a fixture the
// forge answers for under its own name, the two are the same string, so a
// driver doing the forbidden thing produces exactly the answer a correct one
// does. Moved is the fixture where they differ: the forge still answers for a
// repository that has been renamed, and answers with the one it became.
//
// A rename is not a hypothetical. It is the case that makes a builder clone the
// address of one repository and file it under the name of another, and it is
// the case a consumer never sees coming, because the forge reports the move and
// the driver is the thing that threw the report away.
func (r *runner) derivedKey(t tb) {
	t.Helper()
	sess := r.session(t, r.s.Moved)

	repo, err := sess.Repository(r.ctx, r.s.Moved)
	require.NoError(t, r.note(err), "the forge answers for %q, which is now %q", r.s.Moved, r.s.MovedTo)
	assert.Equal(t, r.s.MovedTo, repo.Key,
		"Repository answered with the key it was ASKED about, %q; the key must be derived from the clone address the forge returned, which names %q",
		r.s.Moved, r.s.MovedTo)

	// And the whole repository must be the one the forge served, not a value
	// half taken from the argument.
	r.assertRepository(t, repo, r.s.MovedTo)
}

// assertRepository is the round trip: both clone addresses must re-derive the
// key the forge says the repository has, so "the forge and tau agree which
// repository this is" is a checked fact. repokey refuses a URL carrying a
// credential, so a successful re-parse is also the check that neither address
// has one spliced into it.
func (r *runner) assertRepository(t tb, repo forge.Repository, want repokey.Key) {
	t.Helper()
	assert.Equal(t, want, repo.Key, "the forge and the caller disagree about which repository this is")
	assert.NotEmpty(t, repo.DefaultBranch, "no default branch; a builder has nothing to check out")
	for label, spelling := range map[string]string{"HTTPS": repo.HTTPS, "SSH": repo.SSH} {
		if !assert.NotEmpty(t, spelling, "%s clone address is empty", label) {
			continue
		}
		got, err := repokey.Parse(spelling)
		if !assert.NoError(t, err, "%s clone address %q does not parse to a key", label, spelling) {
			continue
		}
		assert.Equal(t, repo.Key, got, "%s clone address names %q, not %q", label, got, repo.Key)
	}
}

// outage is the invariant that matters most, asserted mechanically rather than
// argued: while the forge is unreachable, every mandatory question is an error
// and none of them is a definitive no.
func (r *runner) outage(t tb) {
	t.Helper()
	sess := r.session(t, r.s.Writable)

	r.s.Break()
	defer r.s.Unbreak()

	lvl, err := sess.Access(r.ctx, r.s.Writable)
	require.Error(t, r.note(err), "an unreachable forge answered the authorization question")
	assert.Equal(t, forge.AccessNone, lvl)
	var nf *forge.NotFoundError
	assert.False(t, errors.As(err, &nf), "an outage must not read as 'no such repository'")

	_, err = sess.Whoami(r.ctx)
	require.Error(t, r.note(err), "an unreachable forge answered Whoami")

	_, err = sess.Repository(r.ctx, r.s.Writable)
	require.Error(t, r.note(err), "an unreachable forge answered Repository")

	if r.s.Expect.Has(forge.CapMembership) {
		ok, err := r.s.Registry.IsActiveMember(r.ctx, r.s.Host, r.s.Namespace, r.s.Member)
		require.Error(t, r.note(err), "an unreachable forge answered a membership question")
		assert.False(t, ok)
	}
}

// credentialNeverTravels reads every request the subject made and every error
// it produced, looking for the credential. A token in a URL is kept by every
// proxy and access log on the path; a token in an error string is kept by
// whoever pasted the error.
func (r *runner) credentialNeverTravels(t tb) {
	t.Helper()
	// One more sweep of ordinary traffic, so the canary reads a full run and
	// not only whatever happened to be left over.
	sess := r.session(t, r.s.Writable)
	r.note(second(sess.Whoami(r.ctx)))
	r.note(second(sess.Access(r.ctx, r.s.Writable)))
	r.note(second(sess.Repository(r.ctx, r.s.Writable)))

	for _, req := range r.s.Requests() {
		u, err := url.Parse(req.URL)
		if !assert.NoError(t, err, "recorded request URL %q does not parse", req.URL) {
			continue
		}
		assert.NotContains(t, req.URL, r.s.Token, "the credential is in a request URL")
		assert.NotContains(t, u.RawQuery, r.s.Token, "the credential is in a query parameter")
		assert.Equal(t, r.s.Host, u.Host, "a request went to %q, which is not the host this credential is for", u.Host)
	}

	for _, err := range r.errs {
		msg := err.Error()
		assert.NotContains(t, msg, r.s.Token, "the credential is in an error string: %s", msg)
		if r.s.BadToken != "" {
			assert.NotContains(t, msg, r.s.BadToken, "the credential is in an error string: %s", msg)
		}
	}
}

// second drops a value and keeps the error, so a call made only for its error
// reads as one line.
func second[T any](_ T, err error) error { return err }

// absent asserts the two halves of a capability a driver does not have: the
// Session method returns *UnsupportedError naming it, and Require refuses.
func (r *runner) absent(t tb, c forge.Capability, err error) {
	t.Helper()
	var un *forge.UnsupportedError
	if assert.ErrorAs(t, r.note(err), &un, "%s is not declared, so it must be *UnsupportedError and never a zero value", c) {
		assert.Equal(t, c, un.Capability, "the refusal names the wrong capability")
		assert.Equal(t, r.s.Host, un.Host)
	}
}

func httpsURL(t tb, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	require.NoError(t, err)
	return u
}
