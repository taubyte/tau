package forgetest

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// commitA is a whole 40-hex object id. Nothing here abbreviates one: an
// abbreviation that later matches two objects is a build of the wrong commit.
const commitA = "1f0c9d2b3a4e5f60718293a4b5c6d7e8f9012345"

// deletedOID is git's deletion sentinel.
const deletedOID = "0000000000000000000000000000000000000000"

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

	t.Run("addressable-refuses-a-depth-it-cannot-address", r.addressableDepth)
	t.Run("a-zero-credential-is-refused-before-the-driver", r.zeroCredential)
	t.Run("anonymous-binds", r.anonymous)
	t.Run("capabilities-are-exactly-what-was-declared", r.capabilities)
	t.Run("whoami", r.whoami)
	t.Run("access", r.access)
	t.Run("repository-round-trips-the-key", r.repository)
	t.Run("the-key-is-derived-from-the-forges-answer", r.derivedKey)
	t.Run("an-outage-is-not-a-refusal", r.outage)
	t.Run("a-redirect-carries-the-credential-nowhere", r.redirectOffHost)
	t.Run("a-same-host-redirect-is-another-repository", r.sameHostRedirect)

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
		t.Fatalf("forgetest: Subject needs Requests, Break and Unbreak; without them a depth refused before any request, and an outage that is not a refusal, are both unassertable")
	}
	if s.RedirectOnce == nil {
		t.Fatalf("forgetest: Subject needs RedirectOnce; without it where a request WENT is unobservable, since a fixture only ever sees the requests that arrived at it")
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
	mustReach(t, err, "binding a session for %q, which every question below is asked through", k)
	return sess
}

// addressableDepth is the anti-GitHub assertion: a forge that takes exactly one
// owner and one name must REFUSE a nested key, before any request, rather than
// join the segments into a name no repository has.
func (r *runner) addressableDepth(t tb) {
	t.Helper()
	mustReach(t, r.driver.Addressable(r.s.Writable), "addressing the writable fixture, which the whole suite is run against")

	r.s.Requests()
	sess, err := r.s.Registry.For(r.s.Deep, r.s.Credential)
	if !r.s.DeepNests {
		var ua *forge.UnaddressableError
		require.ErrorAs(t, r.note(err), &ua, "a forge that does not nest must refuse %q", r.s.Deep)
		assert.Empty(t, r.s.Requests(), "the depth was refused only after a request went out; it must be refused before the wire")
		return
	}

	require.NoError(t, err, "a nesting forge must address %q", r.s.Deep)
	lvl, err := sess.Access(r.ctx, r.s.Deep)
	mustReach(t, r.note(err), "reading the grant on the nested fixture")
	mustHold(t, lvl == forge.AccessAdmin, "a nested fixture this credential holds admin on, which is what the round trip below needs; %q reads as %s", r.s.Deep, lvl)

	repo, err := sess.Repository(r.ctx, r.s.Deep)
	mustReach(t, r.note(err), "reading the record of the nested fixture")
	r.assertRepository(t, repo, r.s.Deep, "the record of the nested fixture")
}

func (r *runner) zeroCredential(t tb) {
	t.Helper()
	// The registry refuses a zero Credential before Bind; this asks the driver's own
	// half of that contract directly.
	require.ErrorIs(t, r.note(second(r.driver.Bind(forge.Credential{}))), forge.ErrNoCredential,
		"Driver.Bind built a client for a credential nobody filled in")
}

func (r *runner) anonymous(t tb) {
	t.Helper()
	// Refusing Anonymous() at Bind would make reading a public repository with no
	// credential an error; what an anonymous session refuses is forge.Session's.
	_, err := r.s.Registry.For(r.s.Writable, forge.Anonymous())
	require.NoError(t, err, "Anonymous() must bind; it is refused at the question, not at the door")
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

	for _, c := range allCapabilities {
		err := sess.Require(c)
		if r.s.Expect.Has(c) {
			assert.NoError(t, err, "Require(%s) refused a capability the session has", c)
			continue
		}
		var un *forge.UnsupportedError
		assert.ErrorAs(t, r.note(err), &un, "Require(%s) must refuse a capability the session lacks", c)
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
	mustReach(t, r.note(err), "asking who this credential is")
	assert.NotEmpty(t, who.Login, "Whoami answered with no login; it is the only honest token check there is")

	bad, err := r.s.Registry.For(r.s.Writable, r.s.BadCredential)
	mustReach(t, err, "binding a session for the credential this forge refuses")
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
	mustReach(t, r.note(err), "reading what this credential holds on the repository it may write")
	assert.True(t, lvl.CanWrite(), "Writable read as %s", lvl)

	// VISIBILITY IS NOT PERMISSION.
	lvl, err = sess.Access(r.ctx, r.s.ReadOnly)
	mustReach(t, r.note(err), "reading what this credential holds on the repository it may only see")
	assert.Equal(t, forge.AccessRead, lvl, "a readable repository with no grant read as %s", lvl)
	assert.False(t, lvl.CanWrite(), "CanWrite() said yes about a grant that is exactly read")

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
	mustReach(t, err, "binding a session for the refused credential, to ask it an authorization question")
	lvl, err = bad.Access(r.ctx, r.s.Writable)
	require.Error(t, r.note(err), "a refused credential must not read as AccessNone")
	assert.Equal(t, forge.AccessNone, lvl, "a refused credential answered a level a caller could act on")
}

func (r *runner) repository(t tb) {
	t.Helper()
	sess := r.session(t, r.s.Writable)
	repo, err := sess.Repository(r.ctx, r.s.Writable)
	mustReach(t, r.note(err), "reading the record the round trip below is about")
	r.assertRepository(t, repo, r.s.Writable, "the record of the writable fixture")

	repo, err = sess.Repository(r.ctx, r.s.Missing)
	var nf *forge.NotFoundError
	require.ErrorAs(t, r.note(err), &nf, "a repository that does not exist was described anyway")
	assert.Equal(t, forge.Repository{}, repo, "a failed Repository returned a value beside its error")
}

// derivedKey: Key must be derived from the clone URL the forge returned, not
// echoed. Only on Moved do the two differ, and a rename is what makes a builder
// clone one repository under another's name.
func (r *runner) derivedKey(t tb) {
	t.Helper()
	sess := r.session(t, r.s.Moved)

	repo, err := sess.Repository(r.ctx, r.s.Moved)
	mustReach(t, r.note(err), "reading the repository the forge still answers for under %q, its old name", r.s.Moved)
	assert.Equal(t, r.s.MovedTo, repo.Key,
		"Repository answered with the key it was ASKED about, %q; the key must be derived from the clone address the forge returned, which names %q",
		r.s.Moved, r.s.MovedTo)

	// And the whole repository must be the one the forge served, not a value
	// half taken from the argument.
	r.assertRepository(t, repo, r.s.MovedTo, "the record the forge answers for a repository that moved")
}

// redirectOffHost starts a host of its own, since a fixture sees only arrivals,
// and points every operation's answer at it: nothing may arrive. It asserts the
// end, not the pin: a driver refusing every 3xx never hands the transport an
// off-host request, so the pin is tested in pkg/forge and the drivers' own
// tests.
func (r *runner) redirectOffHost(t tb) {
	t.Helper()
	d := newDecoy()
	defer d.close()

	for _, op := range r.everyOperation(t) {
		t.Run(op.name, func(t tb) {
			// Everything Requests reports after this belongs to the redirect,
			// which matters for a subject that answers RedirectOnce by
			// reporting the request it would have sent rather than by sending
			// one.
			r.s.Requests()
			// 307 everywhere here: the destination is not a forge, so the only
			// thing the status changes is what arrives at the decoy — and a
			// method-preserving one makes the arrival say which operation
			// leaked rather than turning every one of them into a GET.
			r.s.RedirectOnce(d.url(), http.StatusTemporaryRedirect)

			assert.Error(t, r.note(op.call()),
				"the forge answered a redirect to %q and %s succeeded anyway, so an answer from another host was taken for the forge's", d.url(), op.name)

			r.assertRequestsStayHome(t, r.s.Requests(), "of the requests an operation made after the forge answered it a redirect")
		})
	}

	t.Run("nothing-arrived-at-the-other-host", func(t tb) {
		for _, got := range d.seen() {
			t.Errorf("a request arrived at %q, which is not the host this credential is for: %s %s", d.host(), got.Method, got.URL)
			// Every header, not just Authorization: Go strips only that one on a cross-host
			// redirect, and a PRIVATE-TOKEN header goes along.
			for name, values := range got.Header {
				for _, v := range values {
					if strings.Contains(v, r.s.Token) {
						t.Errorf("and it carried the credential there, in %s", name)
					}
				}
			}
		}
	})
}

// operation is one call the suite can make through the interface, named so a
// failure says which one went somewhere it should not have.
type operation struct {
	name string
	call func() error
}

// everyOperation is every call that reaches the wire. An absent capability is
// refused before any request, and CloneAddress talks to nothing, so neither is
// armed.
func (r *runner) everyOperation(t tb) []operation {
	t.Helper()
	// Every session is bound HERE, on the goroutine and the tb this function was
	// called with. A require inside one of the closures below would otherwise
	// fail a subtest from a tb that is not its own.
	sess := r.session(t, r.s.Writable)
	admin := r.session(t, r.s.Admin)
	creator := r.session(t, r.s.Creatable)
	deleter := r.session(t, r.s.Deletable)
	const pub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIforgetestpublichalf forgetest"
	const noSuchHandle = forge.Handle("forgetest-no-such-handle")

	ops := []operation{
		{"whoami", func() error { return second(sess.Whoami(r.ctx)) }},
		{"access", func() error { return second(sess.Access(r.ctx, r.s.Writable)) }},
		{"repository", func() error { return second(sess.Repository(r.ctx, r.s.Writable)) }},
	}
	if r.s.Expect.Has(forge.CapList) {
		ops = append(ops, operation{"list", func() error {
			return sess.ListRepositories(r.ctx, func(forge.Repository) bool { return true })
		}})
	}
	if r.s.Expect.Has(forge.CapCreate) {
		ops = append(ops, operation{"create", func() error {
			return second(creator.Create(r.ctx, r.s.Creatable, forge.CreateOptions{Description: "forgetest"}))
		}})
	}
	if r.s.Expect.Has(forge.CapDelete) {
		// Deletable, and before the subtest that consumes it: a refused hop
		// deletes nothing, and a driver that followed one deleted nothing here
		// either — the decoy is not a forge.
		ops = append(ops, operation{"delete", func() error {
			return deleter.Delete(r.ctx, r.s.Deletable)
		}})
	}
	if r.s.Expect.Has(forge.CapDeployKeys) {
		ops = append(ops,
			operation{"install-deploy-key", func() error {
				return second(admin.InstallDeployKey(r.ctx, r.s.Admin, "forgetest-decoy", pub))
			}},
			operation{"remove-deploy-key", func() error {
				return admin.RemoveDeployKey(r.ctx, r.s.Admin, noSuchHandle)
			}})
	}
	if r.s.Expect.Has(forge.CapPushHooks) {
		if r.s.Expect.Has(forge.CapSignatures) {
			secret, err := forge.NewSecret()
			mustReach(t, err, "minting the secret the hook the decoy probe registers is keyed with")
			callback := httpsURL(t, "https://build.example.invalid/hooks/forgetest")
			ops = append(ops, operation{"register-push-hook", func() error {
				return second(admin.RegisterPushHook(r.ctx, r.s.Admin, callback, secret))
			}})
		}
		ops = append(ops, operation{"remove-push-hook", func() error {
			return admin.RemovePushHook(r.ctx, r.s.Admin, noSuchHandle)
		}})
	}
	if r.s.Expect.Has(forge.CapPushProbe) {
		ops = append(ops, operation{"can-push", func() error {
			return second(admin.CanPush(r.ctx, r.s.Admin, r.s.PushKey))
		}})
	}
	if r.s.Expect.Has(forge.CapMembership) {
		// THE OTHER CREDENTIAL. Membership is answered by the one the
		// DEPLOYMENT holds, on a client no session can reach, and a redirect
		// policy that lives on the session's client and not on that one is a
		// policy this probe is the only thing standing under.
		ops = append(ops, operation{"membership", func() error {
			return second(r.s.Registry.IsActiveMember(r.ctx, r.s.Host, r.s.Namespace, r.s.Member))
		}})
	}
	return ops
}

// sameHostRedirect: a hop to another repository on this host passes the pin by
// design, so only the driver's redirect policy can refuse it. Repository may
// resolve the hop through its derived key; Access and the three writes have
// nowhere to report it, so they must refuse. Each operation is aimed at the
// address its own endpoint would use, read back from Requests with the wire
// broken; a read is armed with 302 and a write with 307, so a write fails on
// policy rather than endpoint shape.
func (r *runner) sameHostRedirect(t tb) {
	t.Helper()
	// Deletable is the one fixture nothing else in the suite has touched yet,
	// and this runs before the subtest that consumes it.
	other := r.s.Deletable
	const pub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIforgetestpublichalf forgetest"

	t.Run("repository", func(t tb) {
		sess := r.session(t, r.s.Writable)
		victim := r.session(t, other)
		elsewhere := r.whereItWouldGo(t, other, func() error {
			return second(victim.Repository(r.ctx, other))
		})
		r.s.Requests()
		r.s.RedirectOnce(elsewhere, http.StatusFound)

		repo, err := sess.Repository(r.ctx, r.s.Writable)
		if err != nil {
			// Refused the hop, which is the other half of the contract.
			r.note(err)
			return
		}
		// It answered, so it followed — the forge's reply to the request it
		// made was a redirect and nothing else. The record must then say which
		// repository it is describing, and that is not the one asked about.
		assert.Equal(t, other, repo.Key,
			"Repository(%q) followed a redirect to %q and answered with the key it was ASKED about; the record describes %q and names %q, so tau files one repository and clones another",
			r.s.Writable, elsewhere, other, repo.Key)
		r.assertRepository(t, repo, other, "the record a followed same-host redirect answered with")
	})

	t.Run("access", func(t tb) {
		sess := r.session(t, r.s.Writable)
		victim := r.session(t, other)
		elsewhere := r.whereItWouldGo(t, other, func() error {
			return second(victim.Access(r.ctx, other))
		})
		r.s.Requests()
		r.s.RedirectOnce(elsewhere, http.StatusFound)

		lvl, err := sess.Access(r.ctx, r.s.Writable)
		if assert.Error(t, r.note(err),
			"Access(%q) followed a redirect to %q and answered %s; an Access has nowhere to say it is about another repository, so this is %q's grant reported as %q's",
			r.s.Writable, elsewhere, lvl, other, r.s.Writable) {
			assert.Equal(t, forge.AccessNone, lvl, "a followed hop answered a level a caller could act on")
		}
	})

	t.Run("install-deploy-key", func(t tb) {
		sess := r.session(t, r.s.Admin)
		if !r.s.Expect.Has(forge.CapDeployKeys) {
			_, err := sess.InstallDeployKey(r.ctx, r.s.Admin, "forgetest", pub)
			r.absent(t, forge.CapDeployKeys, err, "InstallDeployKey under a same-host redirect")
			return
		}
		victim := r.session(t, other)
		elsewhere := r.whereItWouldGo(t, other, func() error {
			return second(victim.InstallDeployKey(r.ctx, other, "forgetest-address", pub))
		})
		r.s.Requests()
		// 307: a POST rewritten to a GET fails because the deploy-key endpoint
		// answers no GET, which says nothing at all about the hop.
		r.s.RedirectOnce(elsewhere, http.StatusTemporaryRedirect)

		h, err := sess.InstallDeployKey(r.ctx, r.s.Admin, "forgetest-redirect", pub)
		if assert.Error(t, r.note(err),
			"InstallDeployKey(%q) followed a redirect to %q and answered with handle %q; a Handle names no repository, so nothing this call returns can say the key was installed on %q",
			r.s.Admin, elsewhere, h, other) {
			return
		}
		// It installed one somewhere. Take it back out rather than leave a
		// machine credential on a repository nobody named.
		r.note(sess.RemoveDeployKey(r.ctx, r.s.Admin, h))
	})

	t.Run("register-push-hook", func(t tb) {
		sess := r.session(t, r.s.Admin)
		callback := httpsURL(t, "https://build.example.invalid/hooks/forgetest")
		secret, err := forge.NewSecret()
		mustReach(t, err, "minting the secret the hook this probe registers is keyed with")

		if !r.s.Expect.Has(forge.CapPushHooks) || !r.s.Expect.Has(forge.CapSignatures) {
			missing := forge.CapPushHooks
			if r.s.Expect.Has(forge.CapPushHooks) {
				missing = forge.CapSignatures
			}
			_, err := sess.RegisterPushHook(r.ctx, r.s.Admin, callback, secret)
			r.absent(t, missing, err, "RegisterPushHook under a same-host redirect")
			return
		}
		victim := r.session(t, other)
		elsewhere := r.whereItWouldGo(t, other, func() error {
			return second(victim.RegisterPushHook(r.ctx, other, callback, secret))
		})
		r.s.Requests()
		// 307, for the reason the deploy key above gives.
		r.s.RedirectOnce(elsewhere, http.StatusTemporaryRedirect)

		hook, err := sess.RegisterPushHook(r.ctx, r.s.Admin, callback, secret)
		if assert.Error(t, r.note(err),
			"RegisterPushHook(%q) followed a redirect to %q and answered with handle %q; forge.Session stamps Hook.Key FROM THE ARGUMENT, so a hook that landed on %q is recorded against %q and no later call can ever find it",
			r.s.Admin, elsewhere, hook.Handle, other, r.s.Admin) {
			return
		}
		r.note(sess.RemovePushHook(r.ctx, r.s.Admin, hook.Handle))
	})

	// Delete runs last of the writes: a driver that replays it at the far end
	// destroys the repository the other probes are pointed at, and a probe
	// whose target has already been deleted passes for the wrong reason.
	t.Run("delete", func(t tb) {
		sess := r.session(t, r.s.Admin)
		if !r.s.Expect.Has(forge.CapDelete) {
			// Aimed at other, not Admin: a Subject declaring less than its driver has
			// really deletes what this names, and Admin is what every later question is
			// about. other is the fixture the suite may destroy, and this runs after
			// everything needing it.
			r.absent(t, forge.CapDelete, sess.Delete(r.ctx, other), "Delete under a same-host redirect")
			return
		}
		victim := r.session(t, other)
		elsewhere := r.whereItWouldGo(t, other, func() error {
			return victim.Delete(r.ctx, other)
		})
		r.s.Requests()
		// 307, so a DELETE arrives as a DELETE. Under 302 it would arrive as a
		// GET, be answered with a repository, and fail on the shape of that
		// answer rather than on the hop.
		r.s.RedirectOnce(elsewhere, http.StatusTemporaryRedirect)

		assert.Error(t, r.note(sess.Delete(r.ctx, r.s.Admin)),
			"Delete(%q) answered a redirect to %q with success; the method was replayed, so what that reports as gone is a repository the caller never named",
			r.s.Admin, elsewhere)

		// And whatever it did, it must not have done it at the far end.
		_, err := r.session(t, other).Repository(r.ctx, other)
		assert.NoError(t, r.note(err),
			"%q is gone: a write was replayed against the repository the redirect named, which the caller never mentioned", other)
	})
}

// whereItWouldGo is the address this operation uses for k, read back from what
// the subject reported with the wire broken, so a destructive operation is never
// let go there. A URL the suite invented would 404 and hide a followed redirect.
// The last request is the operation's own.
func (r *runner) whereItWouldGo(t tb, k repokey.Key, do func() error) string {
	t.Helper()
	r.s.Requests()
	r.s.Break()
	err := r.note(do())
	r.s.Unbreak()

	// The outage is what this call is expected to fail on, and it composed its
	// request before it did. A session that refuses the capability composed
	// nothing, and that is a different sentence.
	mustAsk(t, err, "learning the address this operation uses for %q, which is where the redirect it is probed with must point", k)

	reqs := r.s.Requests()
	mustHold(t, len(reqs) > 0,
		"reading back the request the operation on %q composed before the wire was broken", k)
	return reqs[len(reqs)-1].URL
}

// assertRepository is the round trip: both clone addresses must re-derive the
// key the forge says the repository has, and repokey refuses a URL carrying a
// credential. about names which of the six records is speaking, so each is a
// separate case.
func (r *runner) assertRepository(t tb, repo forge.Repository, want repokey.Key, about string) {
	t.Helper()
	assert.Equal(t, want, repo.Key, "the forge and the caller disagree about which repository %s is", about)
	r.assertRecord(t, repo, about)
}

// assertRecord asks only whether a record agrees with itself: the listing finds
// each record by its key, so comparing that key to the one asked about would be
// vacuous.
func (r *runner) assertRecord(t tb, repo forge.Repository, about string) {
	t.Helper()
	assert.NotEmpty(t, repo.DefaultBranch, "%s has no default branch; a builder has nothing to check out", about)
	for label, spelling := range map[string]string{"HTTPS": repo.HTTPS, "SSH": repo.SSH} {
		mustHold(t, spelling != "", "reading the %s clone address out of %s, %q", label, about, repo.Key)
		got, err := r.s.Registry.Key(spelling)
		if !assert.NoError(t, err, "the %s clone address in %s, %q, does not parse to a key; repokey refuses a URL carrying a credential, so a token spliced into a record arrives here", label, about, spelling) {
			continue
		}
		assert.Equal(t, repo.Key, got,
			"the %s clone address in %s for %q names %q; tau would file one repository and clone another",
			label, about, repo.Key, got)
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
	assert.Equal(t, forge.AccessNone, lvl, "an unreachable forge answered a level a caller could act on")
	var nf *forge.NotFoundError
	assert.False(t, errors.As(err, &nf), "an outage must not read as 'no such repository'")

	// assert and not require for the three below: they are independent
	// questions, a driver that answers one from a cache is likely to answer all
	// of them, and a short circuit at the first would leave the other two
	// unasked by the very subject that proves them.
	_, err = sess.Whoami(r.ctx)
	assert.Error(t, r.note(err), "an unreachable forge answered Whoami")

	_, err = sess.Repository(r.ctx, r.s.Writable)
	assert.Error(t, r.note(err), "an unreachable forge answered Repository")

	if r.s.Expect.Has(forge.CapMembership) {
		ok, err := r.s.Registry.IsActiveMember(r.ctx, r.s.Host, r.s.Namespace, r.s.Member)
		assert.Error(t, r.note(err), "an unreachable forge answered a membership question")
		assert.False(t, ok, "an unreachable forge answered a membership a caller could act on")
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

	r.assertRequestsStayHome(t, r.s.Requests(), "of everything the subject sent over a whole run")

	for _, err := range r.errs {
		msg := err.Error()
		assert.NotContains(t, msg, r.s.Token, "the live credential is in an error string: %s", msg)
		if r.s.BadToken != "" {
			assert.NotContains(t, msg, r.s.BadToken, "the refused credential is in an error string: %s", msg)
		}
	}
}

// assertRequestsStayHome is what every reported request must satisfy; for a
// subject reporting what a server received, the decoy covers the rest.
func (r *runner) assertRequestsStayHome(t tb, reqs []Request, about string) {
	t.Helper()
	for _, req := range reqs {
		u, err := url.Parse(req.URL)
		mustReach(t, err, "parsing the request URL %q the subject reported", req.URL)

		assert.NotContains(t, req.URL, r.s.Token, "the credential is in a request URL, %s", about)
		assert.NotContains(t, u.RawQuery, r.s.Token, "the credential is in a query parameter, %s", about)
		assert.Equal(t, r.s.Host, u.Host, "a recorded request went to %q, which is not the host this credential is for, %s", u.Host, about)
	}
}

// second drops a value and keeps the error, so a call made only for its error
// reads as one line.
func second[T any](_ T, err error) error { return err }

// absent asserts both halves of a missing capability: the Session method returns
// *UnsupportedError naming it, and Require refuses. about names which of the
// eleven calls refused.
func (r *runner) absent(t tb, c forge.Capability, err error, about string) {
	t.Helper()
	var un *forge.UnsupportedError
	assert.ErrorAs(t, r.note(err), &un, "%s is not declared, so %s must be *UnsupportedError and never a zero value", c, about)
}

func httpsURL(t tb, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	mustReach(t, err, "parsing %q, the callback a hook is registered with", s)
	return u
}
