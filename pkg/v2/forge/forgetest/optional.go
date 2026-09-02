package forgetest

import (
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/v2/forge"
)

// Every function here runs one of TWO contracts and never zero. Absence is a
// tested behaviour: a driver that quietly does not declare a capability it
// meant to implement fails here, which is the only place that failure is
// visible.

func (r *runner) list(t tb) {
	t.Helper()
	sess := r.session(t, r.s.Writable)
	if !r.s.Expect.Has(forge.CapList) {
		r.absent(t, forge.CapList, sess.ListRepositories(r.ctx, func(forge.Repository) bool { return true }), "ListRepositories")
		return
	}

	var seen []forge.Repository
	mustReach(t, r.note(sess.ListRepositories(r.ctx, func(repo forge.Repository) bool {
		seen = append(seen, repo)
		return true
	})), "listing the repositories every question below is about")
	require.NotEmpty(t, seen, "the listing yielded nothing, and Writable is reachable by this credential")

	found := false
	for _, repo := range seen {
		if repo.Key == r.s.Writable {
			found = true
		}
		r.assertRecord(t, repo, "a record the listing yielded")
	}
	assert.True(t, found, "the listing omitted %q, which this credential can write", r.s.Writable)

	// Returning false stops the iteration, and stopping is not an error.
	calls := 0
	mustReach(t, r.note(sess.ListRepositories(r.ctx, func(forge.Repository) bool {
		calls++
		return false
	})), "listing again, to find out whether a false from yield stops it")

	assert.Equal(t, 1, calls, "yield returned false and the listing kept going")

	r.s.Break()
	err := sess.ListRepositories(r.ctx, func(forge.Repository) bool { return true })
	r.s.Unbreak()
	assert.Error(t, r.note(err), "an unreachable forge listed repositories")
}

func (r *runner) create(t tb) {
	t.Helper()
	sess := r.session(t, r.s.Creatable)
	opts := forge.CreateOptions{Description: "forgetest", Private: true}
	if !r.s.Expect.Has(forge.CapCreate) {
		_, err := sess.Create(r.ctx, r.s.Creatable, opts)
		r.absent(t, forge.CapCreate, err, "Create")
		return
	}

	repo, err := sess.Create(r.ctx, r.s.Creatable, opts)
	mustReach(t, r.note(err), "creating the repository whose record is checked below")
	// The key INCLUDING ITS NAMESPACE is what was asked for. A forge that can
	// only create under the authenticated account must refuse a namespaced key,
	// not create the repository somewhere else.
	r.assertRepository(t, repo, r.s.Creatable, "the record of the repository just created")

	_, err = sess.Create(r.ctx, r.s.Creatable, opts)
	assert.Error(t, r.note(err), "creating the same repository twice succeeded")

	un := r.session(t, r.s.Uncreatable)
	_, err = un.Create(r.ctx, r.s.Uncreatable, opts)
	assert.Error(t, r.note(err), "created a repository in a namespace this credential may not write")
}

func (r *runner) delete(t tb) {
	t.Helper()
	sess := r.session(t, r.s.Deletable)
	if !r.s.Expect.Has(forge.CapDelete) {
		r.absent(t, forge.CapDelete, sess.Delete(r.ctx, r.s.Deletable), "Delete")
		return
	}

	// Deleting without admin must fail rather than silently succeed: AccessAdmin
	// is a real rung and this is the method that consumes it.
	writable := r.session(t, r.s.Writable)
	lvl, err := writable.Access(r.ctx, r.s.Writable)
	mustReach(t, r.note(err), "reading what this credential holds on the writable fixture, which decides whether a delete without admin can be asked about")
	if !lvl.CanAdmin() {
		assert.Error(t, r.note(writable.Delete(r.ctx, r.s.Writable)),
			"deleted a repository this credential holds only %s on", lvl)
	}

	assert.NoError(t, r.note(sess.Delete(r.ctx, r.s.Deletable)),
		"deleting a repository this credential holds admin on failed")
	_, err = sess.Repository(r.ctx, r.s.Deletable)
	assert.Error(t, r.note(err), "the repository is still there after a successful delete")
}

func (r *runner) deployKeys(t tb) {
	t.Helper()
	sess := r.session(t, r.s.Admin)
	const pub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIforgetestpublichalf forgetest"
	if !r.s.Expect.Has(forge.CapDeployKeys) {
		_, err := sess.InstallDeployKey(r.ctx, r.s.Admin, "forgetest", pub)
		r.absent(t, forge.CapDeployKeys, err, "InstallDeployKey")
		r.absent(t, forge.CapDeployKeys, sess.RemoveDeployKey(r.ctx, r.s.Admin, "nope"), "RemoveDeployKey")
		return
	}

	h, err := sess.InstallDeployKey(r.ctx, r.s.Admin, "forgetest", pub)
	mustReach(t, r.note(err), "installing the deploy key the questions below are about")
	require.NotEmpty(t, h, "an empty handle is a key nothing can ever remove")
	assert.True(t, r.s.DeployKeyInstalled(h), "the handle names no key on the forge")

	// EVERY KEY THIS INSTALLS IS READ-ONLY. There is no parameter to change it,
	// so a writable one means the driver reached past the interface.
	assert.True(t, r.s.LastDeployKeyReadOnly(),
		"the installed deploy key can push; tau's deploy key exists so a builder can clone")

	assert.Error(t, r.note(sess.RemoveDeployKey(r.ctx, r.s.Admin, "forgetest-no-such-handle")),
		"removing a key that is not there succeeded")

	assert.NoError(t, r.note(sess.RemoveDeployKey(r.ctx, r.s.Admin, h)),
		"removing the deploy key this driver just installed failed; the handle it answered with names nothing the forge will remove")
	assert.False(t, r.s.DeployKeyInstalled(h), "the key is still installed after a successful removal")
}

func (r *runner) cloneAddress(t tb) {
	t.Helper()
	sess := r.session(t, r.s.Writable)
	if !r.s.Expect.Has(forge.CapCloneAddr) {
		_, err := sess.CloneAddress(r.s.Writable, forge.HTTPS)
		r.absent(t, forge.CapCloneAddr, err, "CloneAddress")
		return
	}

	https, err := sess.CloneAddress(r.s.Writable, forge.HTTPS)
	mustReach(t, r.note(err), "asking for the HTTPS clone address a builder would be handed")
	assert.NotEmpty(t, https.HTTPUsername, "no HTTP username; a token needs an account to be presented as")
	r.assertAddress(t, https.URL, r.s.Writable, "HTTPS")

	ssh, err := sess.CloneAddress(r.s.Writable, forge.SSH)
	mustReach(t, r.note(err), "asking for the SSH clone address a builder would be handed")
	assert.NotEmpty(t, ssh.SSHAccount, "no SSH account; the account before the '@' is a host fact")
	r.assertAddress(t, ssh.URL, r.s.Writable, "SSH")
}

// assertAddress re-derives the key from the address. repokey refuses a URL
// carrying a credential, so a successful parse is also the assertion that no
// token has been spliced into the remote — which is what puts one in
// .git/config, in process arguments and in every error quoting the remote.
func (r *runner) assertAddress(t tb, spelling string, want repokey.Key, label string) {
	t.Helper()
	got, err := repokey.Parse(spelling)
	if !assert.NoError(t, err, "the %s clone address handed to a builder, %q, does not parse to a key; repokey refuses a URL carrying a credential, so a token spliced into a remote arrives here", label, spelling) {
		return
	}
	assert.Equal(t, want, got,
		"the %s clone address handed to a builder for %q names %q; that address is the one a checkout actually uses",
		label, want, got)
}

func (r *runner) pushProbe(t tb) {
	t.Helper()
	sess := r.session(t, r.s.Admin)
	if !r.s.Expect.Has(forge.CapPushProbe) {
		_, err := sess.CanPush(r.ctx, r.s.Admin, []byte("no key"))
		r.absent(t, forge.CapPushProbe, err, "CanPush")
		return
	}

	ok, err := sess.CanPush(r.ctx, r.s.Admin, r.s.PushKey)
	mustReach(t, r.note(err), "probing the admin fixture with the git credential that may push")
	assert.True(t, ok, "a credential that may push read as one that may not")

	ok, err = sess.CanPush(r.ctx, r.s.Admin, r.s.NoPushKey)
	mustReach(t, r.note(err), "probing the admin fixture with the git credential that may not push")
	assert.False(t, ok, "a credential that may not push read as one that may")

	missing := r.session(t, r.s.Missing)
	_, err = missing.CanPush(r.ctx, r.s.Missing, r.s.PushKey)
	assert.Error(t, r.note(err), "probing a repository that does not exist must not answer false")
}

func (r *runner) membership(t tb) {
	t.Helper()
	if !r.s.Expect.Has(forge.CapMembership) {
		_, err := r.s.Registry.IsActiveMember(r.ctx, r.s.Host, "acme", "someone")
		r.absent(t, forge.CapMembership, err, "IsActiveMember")
		return
	}

	ok, err := r.s.Registry.IsActiveMember(r.ctx, r.s.Host, r.s.Namespace, r.s.Member)
	mustReach(t, r.note(err), "asking whether the member fixture is a member of %q", r.s.Namespace)
	assert.True(t, ok, "a member of %q read as a non-member", r.s.Namespace)

	// (false, nil) is the ONLY definitive non-member, and everything else is an
	// error: a 403 means the app was blocked, which is an operator problem and
	// not a verdict on the user.
	ok, err = r.s.Registry.IsActiveMember(r.ctx, r.s.Host, r.s.Namespace, r.s.NonMember)
	mustReach(t, r.note(err), "asking whether the non-member fixture is a member of %q", r.s.Namespace)
	assert.False(t, ok, "a non-member of %q read as a member", r.s.Namespace)
}

// pushHooks is the suite's other centrepiece: the whole registration-to-delivery
// round trip, and then every way a delivery can be wrong.
func (r *runner) pushHooks(t tb) {
	t.Helper()
	sess := r.session(t, r.s.Admin)
	callback := httpsURL(t, "https://build.example.invalid/hooks/forgetest")
	secret, err := forge.NewSecret()
	mustReach(t, err, "minting the secret the hook this whole round trip is about is keyed with")

	if !r.s.Expect.Has(forge.CapPushHooks) || !r.s.Expect.Has(forge.CapSignatures) {
		_, err := sess.RegisterPushHook(r.ctx, r.s.Admin, callback, secret)
		missing := forge.CapPushHooks
		if r.s.Expect.Has(forge.CapPushHooks) {
			missing = forge.CapSignatures
		}
		r.absent(t, missing, err, "RegisterPushHook")
		if !r.s.Expect.Has(forge.CapPushHooks) {
			r.absent(t, forge.CapPushHooks, sess.RemovePushHook(r.ctx, r.s.Admin, "nope"), "RemovePushHook")
		}
		// A registry with no Signatures face must refuse to read a delivery at
		// all, rather than read one it cannot have verified the shape of.
		if !r.s.Expect.Has(forge.CapSignatures) {
			_, err := r.s.Registry.VerifyPush(forge.Hook{
				Key: r.s.Admin, Handle: "nope", Scheme: forge.SchemeHubSHA256, Secret: secret,
			}, forge.Delivery{})
			r.absent(t, forge.CapSignatures, err, "VerifyPush")
		}
		return
	}

	hook, err := sess.RegisterPushHook(r.ctx, r.s.Admin, callback, secret)
	mustReach(t, r.note(err), "registering the hook every delivery below is signed for")
	// Key, scheme, secret and a non-empty handle are stamped by forge.Session
	// itself, from the argument and from the driver's own Scheme();
	// TestAHookIsStampedWithWhatItWasAskedFor and
	// TestRegisterPushHookRefusesEveryUnsafeRegistration stand on that. What is
	// the driver's is the handle naming something the forge will answer for.
	assert.True(t, r.s.HookInstalled(hook.Handle), "the handle names no hook on the forge")

	// The forge must hold EXACTLY the wire string the scheme derives. A driver
	// that re-encodes it registers a hook cleanly and then rejects every
	// delivery it ever receives.
	wantWire, err := hook.Scheme.Wire(secret)
	mustReach(t, err, "deriving the wire form the forge should have been handed")
	assert.Equal(t, wantWire, r.s.LastHookWire(), "the forge was given a wire secret the verifier will not key its MAC with")

	r.deliveries(t, hook)

	assert.Error(t, r.note(sess.RemovePushHook(r.ctx, r.s.Admin, "forgetest-no-such-handle")),
		"removing a hook that is not there succeeded")
	assert.NoError(t, r.note(sess.RemovePushHook(r.ctx, r.s.Admin, hook.Handle)),
		"removing the hook this driver just registered failed; the handle it answered with names nothing the forge will remove")
	assert.False(t, r.s.HookInstalled(hook.Handle), "the hook is still registered after a successful removal")
}

// deliveries runs one correct delivery and then every wrong one. VerifyPush is
// the only way a Push exists, so each negative asserts both that it errored and
// that it produced nothing.
func (r *runner) deliveries(t tb, hook forge.Hook) {
	t.Helper()
	const ref = "refs/heads/main"

	good, err := r.s.Delivery(hook, ref, commitA)
	mustReach(t, err, "building the correctly signed delivery the round trip is about")

	push, err := r.s.Registry.VerifyPush(hook, good)
	require.NoError(t, r.note(err), "a correctly signed delivery was rejected")
	// Ref and Commit are the DRIVER'S reading of the payload — the one thing it
	// contributes to a verified delivery — so this is not a stamping check.
	assert.Equal(t, forge.Push{Key: hook.Key, Ref: ref, Commit: commitA}, push,
		"the driver read a push the payload does not describe")

	// EVERY WAY A DELIVERY CAN BE MALFORMED IS ASSERTED ONE PACKAGE DOWN, in
	// pkg/v2/forge's TestEverySchemeRefusesEveryWayADeliveryCanBeWrong, and it
	// belongs there. A flipped byte, a truncated header, a signature under
	// another secret: the core refuses all of them BEFORE any driver code runs,
	// so no fault a driver or a probe can carry reaches those assertions. Here
	// they were seven rows nothing could kill and every one of them was
	// individually deletable with the tree green; there the code that refuses is
	// the code under test, and they run against all four schemes instead of
	// against whichever one this driver happens to use.
	//
	// What stays here is what the SUBJECT decides: the correctly signed
	// delivery, the deputy that names another repository, the deletion
	// sentinel, the delivery that is not a push, and the dated ones — each of
	// them built by a probe a driver author wired up, each of them with a fault
	// behind it in this suite's own table.
	reject := func(name string, h forge.Hook, d forge.Delivery) {
		t.Helper()
		got, err := r.s.Registry.VerifyPush(h, d)
		assert.Error(t, r.note(err), "%s was accepted", name)
		assert.Equal(t, forge.Push{}, got, "%s produced a Push beside its error", name)
	}

	// THE HOOK MAY ONLY SPEAK FOR ITS OWN REPOSITORY.
	deputy, err := r.s.DeliveryFor(hook, r.s.ReadOnly, ref, commitA)
	mustReach(t, err, "building a perfectly signed delivery that speaks for another repository")
	got, err := r.s.Registry.VerifyPush(hook, deputy)
	var ve *forge.VerificationError
	// The comparison is the core's, between the hook's key and the claim the
	// driver read. A driver has no way to make this one pass: it never sees the
	// hook.
	if assert.ErrorAs(t, r.note(err), &ve, "a perfectly signed delivery naming another repository was accepted") {
		assert.Contains(t, ve.Reason, "claims repository",
			"the refusal does not say the delivery claimed another repository, so it was refused for some other reason and DeliveryFor proves nothing")
	}
	assert.Equal(t, forge.Push{}, got, "a delivery naming another repository produced a Push beside its error")

	// A ref deletion is a legitimate delivery with nothing to build, and it has
	// its own error so a receiver answers 200 and drops it.
	deleted, err := r.s.Delivery(hook, ref, deletedOID)
	mustReach(t, err, "building the delivery that reports a ref deletion")
	_, err = r.s.Registry.VerifyPush(hook, deleted)
	assert.ErrorIs(t, r.note(err), forge.ErrRefDeleted, "a ref deletion must be distinguishable from a failure")

	nonPush, err := r.s.NonPushDelivery(hook)
	mustReach(t, err, "building a correctly signed delivery that is not a push at all")
	_, err = r.s.Registry.VerifyPush(hook, nonPush)
	assert.ErrorIs(t, r.note(err), forge.ErrNotAPush, "a delivery that is not a push read as one")

	if hook.Scheme == forge.SchemeStandardWebhooks {
		stale, err := r.s.DeliveryAt(hook, ref, commitA, time.Now().Add(-time.Hour))
		mustReach(t, err, "dating a delivery an hour into the past")
		reject("a delivery signed an hour ago", hook, stale)

		future, err := r.s.DeliveryAt(hook, ref, commitA, time.Now().Add(time.Hour))
		mustReach(t, err, "dating a delivery an hour into the future")
		reject("a delivery signed an hour from now", hook, future)
	}
}
