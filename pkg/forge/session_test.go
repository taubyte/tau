package forge

import (
	"context"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// THE REFUSALS THAT ARE THIS PACKAGE'S AND NOT A DRIVER'S.
//
// Everything here is decided before any driver code runs — by Registry.bind, by
// Session's anonymous guard, by Session.check, by the stamping RegisterPushHook
// does — and that is why it is tested HERE. The conformance suite next door was
// the only thing asserting several of these, which put them in a file whose own
// rule is that every assertion must have a fault behind it: no driver can make
// one of these fire, so no fault could ever be written for one, and the suite
// could not tell that from decoration. They are the same claims, moved to where
// the code that can break them lives.

func sessionForTest(t *testing.T, d Driver, cred Credential) (*Registry, *Session, repokey.Key) {
	t.Helper()
	reg, err := New(Policy{}, Binding{Host: stubHost, Driver: d})
	require.NoError(t, err)
	k := key(t, "https://"+stubHost+"/acme/pay")
	sess, err := reg.For(k, cred)
	require.NoError(t, err)
	return reg, sess, k
}

// TestAZeroCredentialIsRefusedBeforeAnyDriverSeesIt. A Credential nobody filled
// in is the shape a dropped configuration value has, and the one thing it must
// never become is anonymity: an anonymous session is a deliberate state with its
// own refusals, and a forgotten token silently becoming one is a caller reading
// a public repository and believing it asked about a private one.
func TestAZeroCredentialIsRefusedBeforeAnyDriverSeesIt(t *testing.T) {
	d := &stub{host: stubHost, scheme: SchemeHubSHA256}
	reg, err := New(Policy{}, Binding{Host: stubHost, Driver: d})
	require.NoError(t, err)

	_, err = reg.For(key(t, "https://"+stubHost+"/acme/pay"), Credential{})
	require.ErrorIs(t, err, ErrNoCredential, "a zero credential bound a session")

	_, err = reg.Host(stubHost, Credential{})
	require.ErrorIs(t, err, ErrNoCredential, "a zero credential bound a host session")
}

// TestAnAnonymousSessionAnswersNoAuthorizationQuestion. Anonymous() BINDS —
// reading a public repository with no credential is a real thing to want — and
// is refused at the QUESTION rather than at the door. The refusal lives here so
// that no driver can forget it and no driver can invent a second version of it.
func TestAnAnonymousSessionAnswersNoAuthorizationQuestion(t *testing.T) {
	_, sess, k := sessionForTest(t, &stub{host: stubHost, scheme: SchemeHubSHA256}, Anonymous())
	assert.True(t, sess.IsAnonymous(), "a session bound to Anonymous() does not say so")

	// The stub answers AccessAdmin to everyone, so an answer here is the guard
	// having been skipped rather than a forge having been strict.
	lvl, err := sess.Access(context.Background(), k)
	require.ErrorIs(t, err, ErrAnonymous)
	assert.Equal(t, AccessNone, lvl, "a refused authorization question still answered a level")

	id, err := sess.Whoami(context.Background())
	require.ErrorIs(t, err, ErrAnonymous)
	assert.Equal(t, Identity{}, id, "a refused identity question still answered an identity")
}

// TestTheThreeMandatoryCapabilitiesAreNotOptional. Identity, access and
// repository are in every computed Set because they are the interface a Client
// IS. A forge added by stubbing the dangerous questions is the failure this
// package was built against, and the shape of that stub would be a Set missing
// one of these.
func TestTheThreeMandatoryCapabilitiesAreNotOptional(t *testing.T) {
	// plain is a driver with no signatures and no memberships, which is what a
	// host with a clone protocol and no webhooks looks like.
	_, sess, _ := sessionForTest(t, &plain{host: stubHost}, Token("t"))
	got := sess.Capabilities()
	for _, c := range []Capability{CapIdentity, CapAccess, CapRepository} {
		assert.True(t, got.Has(c), "%s is missing from %s, and it is not an optional face", c, got)
	}
	require.NoError(t, sess.Require(CapIdentity, CapAccess, CapRepository),
		"the three questions a Client IS were refused")
}

// TestARefusalNamesTheHostAsWellAsTheQuestion. An *UnsupportedError that names
// only the capability tells an operator which question went unanswered and not
// which deployment could not answer it, and a deployment binds more than one
// host.
func TestARefusalNamesTheHostAsWellAsTheQuestion(t *testing.T) {
	_, sess, k := sessionForTest(t, &plain{host: stubHost}, Token("t"))

	var un *UnsupportedError
	require.ErrorAs(t, sess.Require(CapList), &un)
	assert.Equal(t, CapList, un.Capability)
	assert.Equal(t, stubHost, un.Host, "the refusal does not say which host could not answer")

	err := sess.Delete(context.Background(), k)
	require.ErrorAs(t, err, &un)
	assert.Equal(t, CapDelete, un.Capability)
	assert.Equal(t, stubHost, un.Host)
}

// TestAnUnaddressableKeyIsNamedByTheRefusal. The error carries the KEY and not
// only a sentence, because the caller that has to act on it — skip this
// repository, tell this operator — has nothing else to act on.
func TestAnUnaddressableKeyIsNamedByTheRefusal(t *testing.T) {
	reg, err := New(Policy{}, Binding{Host: stubHost, Driver: &stub{host: stubHost, scheme: SchemeHubSHA256}})
	require.NoError(t, err)
	deep := key(t, "https://"+stubHost+"/acme/team/widget")

	var ua *UnaddressableError
	_, err = reg.For(deep, Token("t"))
	require.ErrorAs(t, err, &ua)
	assert.Equal(t, deep, ua.Key, "the refusal does not name the key it refused")
	assert.Equal(t, stubHost, ua.Host)
}

// TestAMembershipQuestionNobodyCanAnswerIsNeverANo. Both halves of it: a host
// with no driver, and a driver with no membership face. (false, nil) is a
// definitive non-member and a caller will act on it, so neither of these may
// produce one.
func TestAMembershipQuestionNobodyCanAnswerIsNeverANo(t *testing.T) {
	reg, err := New(Policy{}, Binding{Host: stubHost, Driver: &plain{host: stubHost}})
	require.NoError(t, err)

	var un *UnsupportedError
	ok, err := reg.IsActiveMember(context.Background(), stubHost, "acme", "alice")
	require.ErrorAs(t, err, &un, "a driver with no membership face answered a membership question")
	assert.Equal(t, CapMembership, un.Capability)
	assert.False(t, ok, "\"this driver cannot answer\" arrived as \"they are not a member\"")

	var nd *NoDriverError
	ok, err = reg.IsActiveMember(context.Background(), "elsewhere.invalid", "acme", "alice")
	require.ErrorAs(t, err, &nd, "a host with no driver answered a membership question")
	assert.False(t, ok, "\"there is no driver for that host\" arrived as \"they are not a member\"")
}

// TestAHookIsStampedWithWhatItWasAskedFor. Key, scheme and secret are put on a
// Hook by this package and never by a driver, which is what makes "a hook whose
// scheme nothing recorded" unconstructible — and what makes a hook that landed
// on another repository impossible to report honestly, since its Key says the
// one that was asked about no matter where it went.
func TestAHookIsStampedWithWhatItWasAskedFor(t *testing.T) {
	secret, err := NewSecret()
	require.NoError(t, err)
	callback, err := url.Parse("https://build.example.invalid/hooks/x")
	require.NoError(t, err)

	_, sess, k := sessionForTest(t, &stub{host: stubHost, scheme: SchemeHubSHA256, handle: "h-7"}, Token("t"))
	hook, err := sess.RegisterPushHook(context.Background(), k, callback, secret)
	require.NoError(t, err)

	assert.Equal(t, k, hook.Key, "the hook was stamped with a repository nobody named")
	assert.Equal(t, SchemeHubSHA256, hook.Scheme)
	assert.True(t, hook.Secret.Equal(secret), "the hook came back keyed with a secret it was not given")
	assert.Equal(t, Handle("h-7"), hook.Handle)
}

// full binds a client with every optional face, each of which records that it
// was reached. It is how the Session's own wrapping of each face is tested: the
// capability is reported, the call arrives, and a key on another host never
// does.
type full struct {
	stub
	calls []string
}

func (d *full) Bind(c Credential) (Client, error) {
	if c.IsZero() {
		return nil, ErrNoCredential
	}
	return &fullClient{stubClient: stubClient{d: &d.stub}, d: d}, nil
}

type fullClient struct {
	stubClient
	d *full
}

func (c *fullClient) saw(name string) { c.d.calls = append(c.d.calls, name) }

func (c *fullClient) ListRepositories(_ context.Context, yield func(Repository) bool) error {
	c.saw("list")
	for _, name := range []string{"pay", "ledger", "never"} {
		if !yield(Repository{Key: repokey.Key{Host: c.d.host, Owner: "acme", Name: name}}) {
			return nil
		}
	}
	return nil
}
func (c *fullClient) Create(_ context.Context, k repokey.Key, _ CreateOptions) (Repository, error) {
	c.saw("create")
	return Repository{Key: k}, nil
}
func (c *fullClient) Delete(context.Context, repokey.Key) error { c.saw("delete"); return nil }
func (c *fullClient) CloneAddress(k repokey.Key, over Protocol) (Address, error) {
	c.saw("clone-address")
	return Address{URL: string(over) + "://" + k.Host + "/" + k.FullName()}, nil
}
func (c *fullClient) CanPush(context.Context, repokey.Key, []byte) (bool, error) {
	c.saw("can-push")
	return true, nil
}

var (
	_ Lister         = (*fullClient)(nil)
	_ Creator        = (*fullClient)(nil)
	_ Deleter        = (*fullClient)(nil)
	_ CloneAddresses = (*fullClient)(nil)
	_ PushProber     = (*fullClient)(nil)
)

// TestEveryFaceIsReachedThroughTheSession. Each optional face a client has is
// reported by Capabilities and reached by its Session method, and listing stops
// when the caller says so rather than when the driver runs out.
func TestEveryFaceIsReachedThroughTheSession(t *testing.T) {
	d := &full{stub: stub{host: stubHost, scheme: SchemeHubSHA256}}
	_, sess, k := sessionForTest(t, d, Token("t"))
	ctx := context.Background()

	require.NoError(t, sess.Require(CapList, CapCreate, CapDelete, CapDeployKeys, CapPushHooks, CapCloneAddr, CapPushProbe, CapSignatures))

	var listed []string
	require.NoError(t, sess.ListRepositories(ctx, func(r Repository) bool {
		listed = append(listed, r.Key.Name)
		return len(listed) < 2
	}))
	assert.Equal(t, []string{"pay", "ledger"}, listed, "listing went past the caller's stop")

	repo, err := sess.Create(ctx, k, CreateOptions{})
	require.NoError(t, err)
	assert.Equal(t, k, repo.Key)
	require.NoError(t, sess.Delete(ctx, k))

	_, err = sess.Repository(ctx, k)
	require.NoError(t, err)
	lvl, err := sess.Access(ctx, k)
	require.NoError(t, err)
	assert.Equal(t, AccessAdmin, lvl)
	id, err := sess.Whoami(ctx)
	require.NoError(t, err)
	assert.Equal(t, "stub", id.Login)

	h, err := sess.InstallDeployKey(ctx, k, "build", "ssh-ed25519 AAAA")
	require.NoError(t, err)
	assert.Equal(t, Handle("k-1"), h)
	_, err = sess.InstallDeployKey(ctx, k, "build", "  ")
	assert.ErrorContains(t, err, "no public half")
	_, err = sess.ListDeployKeys(ctx, k)
	require.NoError(t, err)
	require.NoError(t, sess.RemoveDeployKey(ctx, k, h))
	_, err = sess.ListPushHooks(ctx, k)
	require.NoError(t, err)
	require.NoError(t, sess.RemovePushHook(ctx, k, "h-1"))

	addr, err := sess.CloneAddress(k, HTTPS)
	require.NoError(t, err)
	assert.Contains(t, addr.URL, k.FullName())
	ok, err := sess.CanPush(ctx, k, []byte("key"))
	require.NoError(t, err)
	assert.True(t, ok)

	assert.Equal(t, []string{"list", "create", "delete", "clone-address", "can-push"}, d.calls)
}

// TestAKeyOnAnotherHostReachesNoFace. Every keyed method runs the same guard,
// so a key on another host is refused by name and the client is never called.
func TestAKeyOnAnotherHostReachesNoFace(t *testing.T) {
	d := &full{stub: stub{host: stubHost, scheme: SchemeHubSHA256}}
	_, sess, _ := sessionForTest(t, d, Token("t"))
	ctx := context.Background()
	other := key(t, "https://elsewhere.invalid/acme/pay")

	errs := []error{}
	collect := func(_ any, err error) { errs = append(errs, err) }
	collect(sess.Access(ctx, other))
	collect(sess.Repository(ctx, other))
	collect(sess.Create(ctx, other, CreateOptions{}))
	collect(nil, sess.Delete(ctx, other))
	collect(sess.InstallDeployKey(ctx, other, "t", "ssh-ed25519 AAAA"))
	collect(sess.ListDeployKeys(ctx, other))
	collect(nil, sess.RemoveDeployKey(ctx, other, "k"))
	collect(sess.ListPushHooks(ctx, other))
	collect(nil, sess.RemovePushHook(ctx, other, "h"))
	collect(sess.CloneAddress(other, SSH))
	collect(sess.CanPush(ctx, other, nil))

	for i, err := range errs {
		var wrong *WrongHostError
		require.ErrorAsf(t, err, &wrong, "call %d", i)
		assert.Equal(t, other, wrong.Key)
	}
	assert.Empty(t, d.calls, "a key on another host reached the client")
}

// TestAMissingFaceIsRefusedForEveryMethod. A client without a face answers
// every method of it with the capability it lacks, never a zero value.
func TestAMissingFaceIsRefusedForEveryMethod(t *testing.T) {
	_, sess, k := sessionForTest(t, &bare{host: stubHost}, Token("t"))
	ctx := context.Background()

	errOf := func(_ any, err error) error { return err }
	want := map[Capability]error{
		CapList:       sess.ListRepositories(ctx, func(Repository) bool { return true }),
		CapCreate:     errOf(sess.Create(ctx, k, CreateOptions{})),
		CapDeployKeys: errOf(sess.InstallDeployKey(ctx, k, "t", "ssh-ed25519 AAAA")),
		CapPushHooks:  errOf(sess.RegisterPushHook(ctx, k, nil, Secret{})),
		CapCloneAddr:  errOf(sess.CloneAddress(k, SSH)),
		CapPushProbe:  errOf(sess.CanPush(ctx, k, nil)),
	}
	for c, err := range want {
		var un *UnsupportedError
		require.ErrorAsf(t, err, &un, "%s", c)
		assert.Equal(t, c, un.Capability)
	}

	for _, err := range []error{
		errOf(sess.ListDeployKeys(ctx, k)),
		sess.RemoveDeployKey(ctx, k, "k"),
	} {
		var un *UnsupportedError
		require.ErrorAs(t, err, &un)
		assert.Equal(t, CapDeployKeys, un.Capability)
	}
	for _, err := range []error{
		errOf(sess.ListPushHooks(ctx, k)),
		sess.RemovePushHook(ctx, k, "h"),
	} {
		var un *UnsupportedError
		require.ErrorAs(t, err, &un)
		assert.Equal(t, CapPushHooks, un.Capability)
	}
}

// bare binds a client with only the three mandatory faces.
type bare struct{ host string }

func (d *bare) Host() string                  { return d.host }
func (d *bare) Addressable(repokey.Key) error { return nil }
func (d *bare) Bind(Credential) (Client, error) {
	return bareClient{host: d.host}, nil
}

type bareClient struct{ host string }

func (c bareClient) Host() string                             { return c.host }
func (c bareClient) Whoami(context.Context) (Identity, error) { return Identity{}, nil }
func (c bareClient) Access(context.Context, repokey.Key) (Access, error) {
	return AccessNone, nil
}
func (c bareClient) Repository(context.Context, repokey.Key) (Repository, error) {
	return Repository{}, nil
}
