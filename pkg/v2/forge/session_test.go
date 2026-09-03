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
