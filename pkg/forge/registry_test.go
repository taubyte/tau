package forge

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// The stubs reach the branches a working forge never does; everything a working
// forge reaches is covered by running pkg/forge/fake through pkg/forge/forgetest.

const stubHost = "git.example.invalid"

type stub struct {
	host        string
	nested      bool
	scheme      Scheme
	bindErr     error
	nilClient   bool
	handle      Handle
	registerErr error
	claim       PushClaim
	pushErr     error
	lastWire    string
	sawScheme   Scheme
	sawBody     []byte
}

func (d *stub) Host() string { return d.host }

func (d *stub) Addressable(k repokey.Key) error {
	if d.nested || !strings.Contains(k.Owner, "/") {
		return nil
	}
	return errors.New("this forge addresses exactly one owner and one name")
}

func (d *stub) Bind(c Credential) (Client, error) {
	if c.IsZero() {
		return nil, ErrNoCredential
	}
	if d.bindErr != nil {
		return nil, d.bindErr
	}
	if d.nilClient {
		return nil, nil
	}
	return &stubClient{d: d}, nil
}

func (d *stub) Scheme() Scheme { return d.scheme }

func (d *stub) Push(body VerifiedBody, _ http.Header) (PushClaim, error) {
	if d.pushErr != nil {
		return PushClaim{}, d.pushErr
	}
	// A driver can only ever read bytes the core verified: VerifiedBody's
	// fields are unexported, so this value cannot have been built anywhere else.
	d.sawScheme, d.sawBody = body.Scheme(), body.Bytes()
	if d.sawScheme == "" || len(d.sawBody) == 0 {
		return PushClaim{}, errors.New("stub: a VerifiedBody arrived empty")
	}
	return d.claim, nil
}

type stubClient struct{ d *stub }

func (c *stubClient) Host() string { return c.d.host }
func (c *stubClient) Whoami(context.Context) (Identity, error) {
	return Identity{Login: "stub"}, nil
}
func (c *stubClient) Access(context.Context, repokey.Key) (Access, error) {
	return AccessAdmin, nil
}
func (c *stubClient) Repository(context.Context, repokey.Key) (Repository, error) {
	return Repository{}, nil
}
func (c *stubClient) RegisterPushHook(_ context.Context, _ repokey.Key, _ *url.URL, wire string) (Handle, error) {
	c.d.lastWire = wire
	return c.d.handle, c.d.registerErr
}
func (c *stubClient) RemovePushHook(context.Context, repokey.Key, Handle) error { return nil }
func (c *stubClient) InstallDeployKey(context.Context, repokey.Key, string, string) (Handle, error) {
	return "k-1", nil
}
func (c *stubClient) RemoveDeployKey(context.Context, repokey.Key, Handle) error { return nil }
func (c *stubClient) ListDeployKeys(context.Context, repokey.Key) ([]InstalledDeployKey, error) {
	return nil, nil
}
func (c *stubClient) ListPushHooks(context.Context, repokey.Key) ([]InstalledPushHook, error) {
	return nil, nil
}

type wanderer struct{ host string }

func (d *wanderer) Host() string                  { return d.host }
func (d *wanderer) Addressable(repokey.Key) error { return nil }
func (d *wanderer) Bind(Credential) (Client, error) {
	return &stubClient{d: &stub{host: "elsewhere.invalid"}}, nil
}

// plain has no Signatures and no Memberships, which is what a host with a
// clone protocol and no webhooks looks like.
type plain struct{ host string }

func (d *plain) Host() string                    { return d.host }
func (d *plain) Addressable(repokey.Key) error   { return nil }
func (d *plain) Bind(Credential) (Client, error) { return &stubClient{d: &stub{host: d.host}}, nil }

var (
	_ Driver     = (*stub)(nil)
	_ Signatures = (*stub)(nil)
	_ Client     = (*stubClient)(nil)
	_ PushHooks  = (*stubClient)(nil)
	_ DeployKeys = (*stubClient)(nil)
	_ Driver     = (*plain)(nil)
)

func key(t *testing.T, spelling string) repokey.Key {
	t.Helper()
	k, err := repokey.Parse(spelling)
	require.NoError(t, err)
	return k
}

func TestNewRefusesADeploymentThatIsWrong(t *testing.T) {
	good := &stub{host: stubHost, scheme: SchemeHubSHA256}

	for name, tc := range map[string]struct {
		policy   Policy
		bindings []Binding
		saying   string
	}{
		"a binding with no driver": {
			bindings: []Binding{{Host: stubHost}},
			saying:   "has no driver",
		},
		"a binding with no host": {
			bindings: []Binding{{Driver: good}},
			saying:   "names no host",
		},
		"a host that is not a repokey spelling": {
			bindings: []Binding{{Host: "GitHub.com", Driver: &stub{host: "GitHub.com"}}},
			saying:   "is not a host spelling repokey produces",
		},
		"a host that is a URL": {
			bindings: []Binding{{Host: "https://github.com", Driver: &stub{host: "https://github.com"}}},
			saying:   "is not a host spelling repokey produces",
		},
		"a host spelling its default port": {
			bindings: []Binding{{Host: "github.com:443", Driver: &stub{host: "github.com:443"}}},
			saying:   "is not a host spelling repokey produces",
		},
		"a credential aimed at the wrong server": {
			bindings: []Binding{{Host: "other.invalid", Driver: good}},
			saying:   "disagrees with driver host",
		},
		"two drivers for one host": {
			bindings: []Binding{
				{Host: stubHost, Driver: good},
				{Host: stubHost, Driver: &stub{host: stubHost, scheme: SchemeHubSHA256}},
			},
			saying: "two drivers bound to host",
		},
		"a scheme this package does not implement": {
			bindings: []Binding{{Host: stubHost, Driver: &stub{host: stubHost, scheme: "invented"}}},
			saying:   "does not implement",
		},
		// The token-replay schemes are gone: a driver naming one names a
		// scheme this package does not implement, under every policy.
		"a token-replay scheme": {
			bindings: []Binding{{Host: stubHost, Driver: &stub{host: stubHost, scheme: "shared-token"}}},
			saying:   "does not implement",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New(tc.policy, tc.bindings...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.saying)
		})
	}

	// A driver with no Signatures face is bound with no scheme question at all.
	_, err := New(Policy{}, Binding{Host: stubHost, Driver: &plain{host: stubHost}})
	require.NoError(t, err)
}

func TestRegistryResolvesOnlyWhatItWasGiven(t *testing.T) {
	reg, err := New(Policy{},
		Binding{Host: "z.invalid", Driver: &plain{host: "z.invalid"}},
		Binding{Host: "a.invalid", Driver: &plain{host: "a.invalid"}},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.invalid", "z.invalid"}, reg.Hosts(), "Hosts is sorted, for a startup line")

	_, err = reg.Driver("a.invalid")
	require.NoError(t, err)

	var nd *NoDriverError
	_, err = reg.Driver("codeberg.org")
	require.ErrorAs(t, err, &nd)
	assert.Equal(t, "codeberg.org", nd.Host)
	assert.Contains(t, nd.Error(), "no driver for host")

	// There is no generic driver behind the miss, and no Register method to put
	// one there later.
	sess, err := reg.Host("a.invalid", Token("t"))
	require.NoError(t, err)
	assert.Equal(t, "a.invalid", sess.Host())
	_, err = reg.Host("codeberg.org", Token("t"))
	require.ErrorAs(t, err, &nd)
}

func TestBindFailuresAreNotSessions(t *testing.T) {
	boom := errors.New("the driver could not build a client")
	reg, err := New(Policy{}, Binding{Host: stubHost, Driver: &stub{host: stubHost, scheme: SchemeHubSHA256, bindErr: boom}})
	require.NoError(t, err)
	_, err = reg.For(key(t, "https://"+stubHost+"/acme/pay"), Token("t"))
	require.ErrorIs(t, err, boom)

	reg, err = New(Policy{}, Binding{Host: stubHost, Driver: &stub{host: stubHost, scheme: SchemeHubSHA256, nilClient: true}})
	require.NoError(t, err)
	_, err = reg.Host(stubHost, Token("t"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil client")

	// A client that answers for another host is a credential aimed at the wrong
	// server, one layer below the binding New already checks.
	reg, err = New(Policy{}, Binding{Host: stubHost, Driver: &wanderer{host: stubHost}})
	require.NoError(t, err)
	_, err = reg.Host(stubHost, Token("t"))
	var leak *CredentialLeakError
	require.ErrorAs(t, err, &leak)
	assert.Equal(t, "elsewhere.invalid", leak.To)
}

func TestASessionRefusesAKeyItWasNotBoundFor(t *testing.T) {
	d := &stub{host: stubHost, scheme: SchemeHubSHA256, handle: "h-1"}
	reg, err := New(Policy{}, Binding{Host: stubHost, Driver: d})
	require.NoError(t, err)

	sess, err := reg.For(key(t, "https://"+stubHost+"/acme/pay"), Token("t"))
	require.NoError(t, err)

	var wh *WrongHostError
	_, err = sess.Repository(context.Background(), key(t, "https://elsewhere.invalid/acme/pay"))
	require.ErrorAs(t, err, &wh)
	assert.Contains(t, wh.Error(), "cannot act on")

	// And the authorization question, which is the one a caller ACTS on: a
	// session walked onto another host's key must not answer it, and must not
	// answer AccessNone either, because that reads as a verdict.
	lvl, err := sess.Access(context.Background(), key(t, "https://elsewhere.invalid/acme/pay"))
	require.ErrorAs(t, err, &wh)
	assert.Equal(t, AccessNone, lvl, "a refused authorization question still answered a level")

	// A depth this driver cannot address, refused at the session too and not
	// only at the registry.
	var ua *UnaddressableError
	_, err = sess.Repository(context.Background(), key(t, "https://"+stubHost+"/acme/team/widget"))
	require.ErrorAs(t, err, &ua)
	assert.Contains(t, ua.Error(), "cannot address")

	_, err = reg.For(key(t, "https://"+stubHost+"/acme/team/widget"), Token("t"))
	require.ErrorAs(t, err, &ua)
}

func TestRegisterPushHookRefusesEveryUnsafeRegistration(t *testing.T) {
	ctx := context.Background()
	k := key(t, "https://"+stubHost+"/acme/pay")
	https, err := url.Parse("https://build.example.invalid/hooks/x")
	require.NoError(t, err)
	secret, err := NewSecret()
	require.NoError(t, err)

	sessionFor := func(t *testing.T, d *stub, p Policy) *Session {
		t.Helper()
		reg, err := New(p, Binding{Host: stubHost, Driver: d})
		require.NoError(t, err)
		sess, err := reg.For(k, Token("t"))
		require.NoError(t, err)
		return sess
	}

	t.Run("no secret", func(t *testing.T) {
		sess := sessionFor(t, &stub{host: stubHost, scheme: SchemeHubSHA256, handle: "h"}, Policy{})
		_, err := sess.RegisterPushHook(ctx, k, https, Secret{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no secret")
	})

	t.Run("no callback", func(t *testing.T) {
		sess := sessionFor(t, &stub{host: stubHost, scheme: SchemeHubSHA256, handle: "h"}, Policy{})
		_, err := sess.RegisterPushHook(ctx, k, nil, secret)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no callback")
	})

	t.Run("a cleartext callback", func(t *testing.T) {
		cleartext, err := url.Parse("http://build.example.invalid/hooks/x")
		require.NoError(t, err)
		sess := sessionFor(t, &stub{host: stubHost, scheme: SchemeHubSHA256, handle: "h"}, Policy{})
		_, err = sess.RegisterPushHook(ctx, k, cleartext, secret)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "AllowInsecureCallback")

		// And accepted when a deployment says so, once, in the policy.
		sess = sessionFor(t, &stub{host: stubHost, scheme: SchemeHubSHA256, handle: "h"},
			Policy{AllowInsecureCallback: true})
		hook, err := sess.RegisterPushHook(ctx, k, cleartext, secret)
		require.NoError(t, err)
		assert.Equal(t, Handle("h"), hook.Handle)
	})

	t.Run("a secret too short to key a MAC, whatever the scheme", func(t *testing.T) {
		for _, sc := range []Scheme{SchemeHubSHA256, SchemeBitbucketSHA256, SchemeStandardWebhooks} {
			sess := sessionFor(t, &stub{host: stubHost, scheme: sc, handle: "h"}, Policy{})
			for _, short := range []string{"x", "short", strings.Repeat("a", SecretSize-1)} {
				_, err := sess.RegisterPushHook(ctx, k, https, SecretFrom([]byte(short)))
				require.Error(t, err, "%s registered a hook with a %d-byte secret", sc, len(short))
				assert.Contains(t, err.Error(), "at least 32 bytes")
			}
		}
	})

	t.Run("a driver that fails", func(t *testing.T) {
		boom := errors.New("the forge said no")
		sess := sessionFor(t, &stub{host: stubHost, scheme: SchemeHubSHA256, registerErr: boom}, Policy{})
		_, err := sess.RegisterPushHook(ctx, k, https, secret)
		require.ErrorIs(t, err, boom)
	})

	t.Run("a handle nothing could ever remove", func(t *testing.T) {
		sess := sessionFor(t, &stub{host: stubHost, scheme: SchemeHubSHA256, handle: ""}, Policy{})
		_, err := sess.RegisterPushHook(ctx, k, https, secret)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty hook handle")
	})

	t.Run("a driver with no Signatures face cannot register one", func(t *testing.T) {
		reg, err := New(Policy{}, Binding{Host: stubHost, Driver: &plain{host: stubHost}})
		require.NoError(t, err)
		sess, err := reg.For(k, Token("t"))
		require.NoError(t, err)
		var un *UnsupportedError
		_, err = sess.RegisterPushHook(ctx, k, https, secret)
		require.ErrorAs(t, err, &un)
		assert.Equal(t, CapSignatures, un.Capability)
	})

	t.Run("the driver is handed the wire string and never the secret", func(t *testing.T) {
		d := &stub{host: stubHost, scheme: SchemeHubSHA256, handle: "h"}
		sess := sessionFor(t, d, Policy{})
		hook, err := sess.RegisterPushHook(ctx, k, https, secret)
		require.NoError(t, err)
		want, err := SchemeHubSHA256.Wire(secret)
		require.NoError(t, err)
		assert.Equal(t, want, d.lastWire)
		assert.NotContains(t, d.lastWire, string(secret.Expose()))
		assert.Equal(t, SchemeHubSHA256, hook.Scheme, "the hook is stamped with the scheme it was registered under")
	})
}

func TestInstallDeployKeyRefusesAKeyWithNoPublicHalf(t *testing.T) {
	d := &stub{host: stubHost, scheme: SchemeHubSHA256}
	reg, err := New(Policy{}, Binding{Host: stubHost, Driver: d})
	require.NoError(t, err)
	k := key(t, "https://"+stubHost+"/acme/pay")
	sess, err := reg.For(k, Token("t"))
	require.NoError(t, err)

	_, err = sess.InstallDeployKey(context.Background(), k, "title", "   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no public half")
}

func TestVerifyPushRefusesEveryWayADeliveryCanBeWrong(t *testing.T) {
	k := key(t, "https://"+stubHost+"/acme/pay")
	secret := vectorSecret()
	const ref = "refs/heads/main"
	const commit = "1f0c9d2b3a4e5f60718293a4b5c6d7e8f9012345"

	signed := func(t *testing.T, sc Scheme, body []byte) Delivery {
		t.Helper()
		hdr, err := Sign(sc, secret, body, "msg_1", time.Now())
		require.NoError(t, err)
		return Delivery{Header: hdr, Body: body}
	}

	newRegistry := func(t *testing.T, d Driver, p Policy) *Registry {
		t.Helper()
		reg, err := New(p, Binding{Host: stubHost, Driver: d})
		require.NoError(t, err)
		return reg
	}

	body := []byte(`{"anything":true}`)
	claim := PushClaim{Repository: k, Ref: ref, Commit: commit}
	hook := Hook{Key: k, Handle: "h", Scheme: SchemeHubSHA256, Secret: secret}

	t.Run("the happy path", func(t *testing.T) {
		d := &stub{host: stubHost, scheme: SchemeHubSHA256, claim: claim}
		reg := newRegistry(t, d, Policy{})
		push, err := reg.VerifyPush(hook, signed(t, SchemeHubSHA256, body))
		require.NoError(t, err)
		assert.Equal(t, Push{Key: k, Ref: ref, Commit: commit}, push)
		// The driver read the body only after the core verified it, and it was
		// handed the scheme the HOOK was recorded under.
		assert.Equal(t, hook.Scheme, d.sawScheme)
		assert.Equal(t, body, d.sawBody)
	})

	t.Run("a host this registry has no driver for", func(t *testing.T) {
		reg := newRegistry(t, &stub{host: stubHost, scheme: SchemeHubSHA256, claim: claim}, Policy{})
		elsewhere := hook
		elsewhere.Key = key(t, "https://other.invalid/acme/pay")
		var nd *NoDriverError
		_, err := reg.VerifyPush(elsewhere, signed(t, SchemeHubSHA256, body))
		require.ErrorAs(t, err, &nd)
	})

	t.Run("a hook key the driver cannot address", func(t *testing.T) {
		reg := newRegistry(t, &stub{host: stubHost, scheme: SchemeHubSHA256, claim: claim}, Policy{})
		deep := hook
		deep.Key = key(t, "https://"+stubHost+"/acme/team/widget")
		var ua *UnaddressableError
		_, err := reg.VerifyPush(deep, signed(t, SchemeHubSHA256, body))
		require.ErrorAs(t, err, &ua)
	})

	t.Run("a driver that cannot read a payload at all", func(t *testing.T) {
		reg := newRegistry(t, &plain{host: stubHost}, Policy{})
		var un *UnsupportedError
		_, err := reg.VerifyPush(hook, signed(t, SchemeHubSHA256, body))
		require.ErrorAs(t, err, &un)
		assert.Equal(t, CapSignatures, un.Capability)
	})

	t.Run("a scheme this package does not implement", func(t *testing.T) {
		reg := newRegistry(t, &stub{host: stubHost, scheme: SchemeHubSHA256, claim: claim}, Policy{})
		invented := hook
		invented.Scheme = "invented"
		var ve *VerificationError
		_, err := reg.VerifyPush(invented, signed(t, SchemeHubSHA256, body))
		require.ErrorAs(t, err, &ve)
		assert.Contains(t, ve.Reason, "not implemented")
	})

	t.Run("a hook stored under a token-replay scheme", func(t *testing.T) {
		reg := newRegistry(t, &stub{host: stubHost, scheme: SchemeHubSHA256, claim: claim}, Policy{})
		for _, old := range []Scheme{"shared-token", "onedev-token", "basic-auth"} {
			weak := hook
			weak.Scheme = old
			d := signed(t, SchemeHubSHA256, body)
			d.Header.Set("X-Gitlab-Token", "anything")
			var ve *VerificationError
			_, err := reg.VerifyPush(weak, d)
			require.ErrorAs(t, err, &ve, string(old))
			assert.Contains(t, ve.Reason, "not implemented")
		}
	})

	t.Run("a hook with no secret", func(t *testing.T) {
		reg := newRegistry(t, &stub{host: stubHost, scheme: SchemeHubSHA256, claim: claim}, Policy{})
		none := hook
		none.Secret = Secret{}
		var ve *VerificationError
		_, err := reg.VerifyPush(none, signed(t, SchemeHubSHA256, body))
		require.ErrorAs(t, err, &ve)
		assert.Contains(t, ve.Reason, "no secret")
	})

	// A hook that predates the floor, or one a consumer built by hand out of a
	// store, never went through RegisterPushHook. Checking strength only at
	// registration would leave that hook verifying forever.
	t.Run("a hook whose stored secret is too short to key a MAC", func(t *testing.T) {
		reg := newRegistry(t, &stub{host: stubHost, scheme: SchemeHubSHA256, claim: claim}, Policy{})
		weak := hook
		weak.Secret = SecretFrom([]byte("x"))
		// Signed with that very secret, so nothing but the floor refuses it.
		hdr, err := Sign(SchemeHubSHA256, vectorSecret(), body, "msg_1", time.Now())
		require.NoError(t, err)
		var ve *VerificationError
		_, err = reg.VerifyPush(weak, Delivery{Header: hdr, Body: body})
		require.ErrorAs(t, err, &ve)
		assert.Contains(t, ve.Reason, "shorter than a MAC key")
	})

	t.Run("a body over the cap, refused before any MAC", func(t *testing.T) {
		reg := newRegistry(t, &stub{host: stubHost, scheme: SchemeHubSHA256, claim: claim},
			Policy{MaxDeliveryBody: len(body) - 1})
		var ve *VerificationError
		_, err := reg.VerifyPush(hook, signed(t, SchemeHubSHA256, body))
		require.ErrorAs(t, err, &ve)
		assert.Contains(t, ve.Reason, "exceeds the cap")
	})

	t.Run("a signature that does not match", func(t *testing.T) {
		reg := newRegistry(t, &stub{host: stubHost, scheme: SchemeHubSHA256, claim: claim}, Policy{})
		d := signed(t, SchemeHubSHA256, body)
		d.Body = append(append([]byte(nil), body...), ' ')
		var ve *VerificationError
		_, err := reg.VerifyPush(hook, d)
		require.ErrorAs(t, err, &ve)
	})

	t.Run("a driver that cannot read the verified payload", func(t *testing.T) {
		boom := errors.New("unreadable payload")
		reg := newRegistry(t, &stub{host: stubHost, scheme: SchemeHubSHA256, pushErr: boom}, Policy{})
		_, err := reg.VerifyPush(hook, signed(t, SchemeHubSHA256, body))
		require.ErrorIs(t, err, boom)
	})

	t.Run("a hook speaking for another repository", func(t *testing.T) {
		deputy := claim
		deputy.Repository = key(t, "https://"+stubHost+"/someone/else")
		reg := newRegistry(t, &stub{host: stubHost, scheme: SchemeHubSHA256, claim: deputy}, Policy{})
		var ve *VerificationError
		_, err := reg.VerifyPush(hook, signed(t, SchemeHubSHA256, body))
		require.ErrorAs(t, err, &ve)
		assert.Contains(t, ve.Reason, "claims repository")
	})

	t.Run("a payload naming no ref", func(t *testing.T) {
		noRef := claim
		noRef.Ref = ""
		reg := newRegistry(t, &stub{host: stubHost, scheme: SchemeHubSHA256, claim: noRef}, Policy{})
		var ve *VerificationError
		_, err := reg.VerifyPush(hook, signed(t, SchemeHubSHA256, body))
		require.ErrorAs(t, err, &ve)
		assert.Contains(t, ve.Reason, "no ref")
	})

	t.Run("a ref deletion is its own answer", func(t *testing.T) {
		deleted := claim
		deleted.Commit = strings.Repeat("0", 40)
		reg := newRegistry(t, &stub{host: stubHost, scheme: SchemeHubSHA256, claim: deleted}, Policy{})
		_, err := reg.VerifyPush(hook, signed(t, SchemeHubSHA256, body))
		require.ErrorIs(t, err, ErrRefDeleted, "a receiver answers 200 and drops it, rather than chasing a failure")
	})

	t.Run("a payload naming no whole commit", func(t *testing.T) {
		for _, spelling := range []string{"", "HEAD", "1f0c9d2"} {
			partial := claim
			partial.Commit = spelling
			reg := newRegistry(t, &stub{host: stubHost, scheme: SchemeHubSHA256, claim: partial}, Policy{})
			var ve *VerificationError
			_, err := reg.VerifyPush(hook, signed(t, SchemeHubSHA256, body))
			require.ErrorAs(t, err, &ve, "commit %q", spelling)
			assert.Contains(t, ve.Reason, "no commit")
		}
	})

	t.Run("a delivery that is not a push", func(t *testing.T) {
		reg := newRegistry(t, &stub{host: stubHost, scheme: SchemeHubSHA256, pushErr: ErrNotAPush}, Policy{})
		_, err := reg.VerifyPush(hook, signed(t, SchemeHubSHA256, body))
		require.ErrorIs(t, err, ErrNotAPush)
	})
}

func TestPolicyClockAndToleranceAreSubstitutable(t *testing.T) {
	k := key(t, "https://"+stubHost+"/acme/pay")
	secret := vectorSecret()
	claim := PushClaim{Repository: k, Ref: "refs/heads/main", Commit: strings.Repeat("ab", 20)}
	hook := Hook{Key: k, Handle: "h", Scheme: SchemeStandardWebhooks, Secret: secret}

	hdr, err := Sign(SchemeStandardWebhooks, secret, []byte(`{}`), "msg_1", vectorAt)
	require.NoError(t, err)
	d := Delivery{Header: hdr, Body: []byte(`{}`)}

	frozen := Policy{Now: func() time.Time { return vectorAt.Add(time.Minute) }}
	reg, err := New(frozen, Binding{Host: stubHost, Driver: &stub{host: stubHost, scheme: SchemeStandardWebhooks, claim: claim}})
	require.NoError(t, err)
	assert.Equal(t, vectorAt.Add(time.Minute), reg.now())
	assert.Equal(t, DefaultTimestampTolerance, reg.tolerance())
	assert.Equal(t, DefaultMaxDeliveryBody, reg.maxDeliveryBody())
	_, err = reg.VerifyPush(hook, d)
	require.NoError(t, err)

	tight := Policy{Now: func() time.Time { return vectorAt.Add(time.Minute) }, Tolerance: time.Second, MaxDeliveryBody: 1 << 20}
	reg, err = New(tight, Binding{Host: stubHost, Driver: &stub{host: stubHost, scheme: SchemeStandardWebhooks, claim: claim}})
	require.NoError(t, err)
	assert.Equal(t, time.Second, reg.tolerance())
	assert.Equal(t, 1<<20, reg.maxDeliveryBody())
	_, err = reg.VerifyPush(hook, d)
	require.Error(t, err, "a one-second window accepted a minute-old delivery")

	// The default clock is the wall clock.
	reg, err = New(Policy{}, Binding{Host: stubHost, Driver: &plain{host: stubHost}})
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), reg.now(), time.Minute)
}

func TestCapabilitySetRenders(t *testing.T) {
	set := Set{CapAccess: {}, CapIdentity: {}, CapRepository: {}, CapPushHooks: {}}
	assert.Equal(t, "access,identity,push-hooks,repository", set.String())
	assert.Equal(t, []Capability{CapAccess, CapIdentity, CapPushHooks, CapRepository}, set.List())
	assert.True(t, set.Has(CapAccess))
	assert.False(t, set.Has(CapList))

	assert.True(t, set.Equal(Set{CapAccess: {}, CapIdentity: {}, CapRepository: {}, CapPushHooks: {}}))
	assert.False(t, set.Equal(Set{CapAccess: {}}), "a smaller set is not equal")
	assert.False(t, set.Equal(Set{CapAccess: {}, CapIdentity: {}, CapRepository: {}, CapList: {}}),
		"the same size with a different member is not equal")
}

func TestErrorsSayWhichQuestionWentUnanswered(t *testing.T) {
	k := key(t, "https://"+stubHost+"/acme/pay")
	for _, tc := range []struct {
		err    error
		saying string
	}{
		{&NoDriverError{Host: stubHost}, "no driver for host"},
		{&UnsupportedError{Host: stubHost, Capability: CapPushHooks}, "cannot answer push-hooks"},
		{&UnaddressableError{Host: stubHost, Key: k, Reason: "one owner"}, "cannot address"},
		{&WrongHostError{Session: stubHost, Key: k}, "cannot act on"},
		{&NotFoundError{Host: stubHost, Key: k}, "serves no repository"},
		{&CredentialError{Host: stubHost, Reason: "expired"}, "refused the credential"},
		{&VerificationError{Scheme: SchemeHubSHA256, Reason: "no match"}, "failed hub-sha256 verification"},
		{&CredentialLeakError{Bound: stubHost, To: "attacker.invalid"}, "refusing to carry"},
	} {
		assert.Contains(t, tc.err.Error(), tc.saying)
	}
}

func TestEverySchemeRefusesEveryWayADeliveryCanBeWrong(t *testing.T) {
	k := key(t, "https://"+stubHost+"/acme/pay")
	body := []byte(`{"anything":true}`)
	claim := PushClaim{Repository: k, Ref: "refs/heads/main", Commit: strings.Repeat("a", 40)}

	for _, scheme := range []Scheme{
		SchemeHubSHA256, SchemeStandardWebhooks, SchemeBitbucketSHA256, SchemeGogsSHA256,
	} {
		t.Run(string(scheme), func(t *testing.T) {
			secret := vectorSecret()
			reg, err := New(Policy{},
				Binding{Host: stubHost, Driver: &stub{host: stubHost, scheme: scheme, claim: claim}})
			require.NoError(t, err)

			hook := Hook{Key: k, Handle: "h", Scheme: scheme, Secret: secret}
			hdr, err := Sign(scheme, secret, body, "msg_1", time.Now())
			require.NoError(t, err)
			good := Delivery{Header: hdr, Body: body}

			push, err := reg.VerifyPush(hook, good)
			require.NoError(t, err, "the correctly signed delivery every mutation below is a mutation OF")
			require.Equal(t, Push{Key: k, Ref: claim.Ref, Commit: claim.Commit}, push)

			other, err := NewSecret()
			require.NoError(t, err)
			underOther := hook
			underOther.Secret = other
			wrongScheme := hook
			wrongScheme.Scheme = otherImplemented(scheme)

			for _, tc := range []struct {
				name string
				hook Hook
				d    Delivery
			}{
				{name: "a body with one byte changed", hook: hook,
					d: Delivery{Header: good.Header.Clone(), Body: flipOne(good.Body)}},
				{name: "a body with one byte appended", hook: hook,
					d: Delivery{Header: good.Header.Clone(), Body: append(append([]byte(nil), good.Body...), ' ')}},
				{name: "a delivery with no headers at all", hook: hook,
					d: Delivery{Body: good.Body}},
				{name: "a delivery whose headers were each truncated by one character", hook: hook,
					d: Delivery{Header: truncateValues(good.Header), Body: good.Body}},
				{name: "a signature under a different secret", hook: underOther, d: good},
				{name: "a hook whose recorded scheme is a different one", hook: wrongScheme, d: good},
			} {
				t.Run(tc.name, func(t *testing.T) {
					got, err := reg.VerifyPush(tc.hook, tc.d)
					assert.Error(t, err, "%s was accepted", tc.name)
					assert.Equal(t, Push{}, got, "%s produced a Push beside its error", tc.name)
				})
			}
		})
	}
}

// otherImplemented is any scheme this package implements that is not s, so "the
// hook was recorded under one procedure and checked under another" can be
// asserted without naming a forge.
func otherImplemented(s Scheme) Scheme {
	for _, candidate := range []Scheme{
		SchemeHubSHA256, SchemeStandardWebhooks, SchemeBitbucketSHA256, SchemeGogsSHA256,
	} {
		if candidate != s {
			return candidate
		}
	}
	return s
}

func flipOne(b []byte) []byte {
	out := append([]byte(nil), b...)
	if len(out) > 0 {
		out[len(out)/2] ^= 0x20
	}
	return out
}

// truncateValues shortens every header value by one character. It is blunt on
// purpose: this does not know which header carries a given scheme's signature,
// and every scheme must refuse a delivery whose headers no longer say what they
// said when it was signed.
func truncateValues(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, vs := range h {
		cut := make([]string, 0, len(vs))
		for _, v := range vs {
			if len(v) > 0 {
				v = v[:len(v)-1]
			}
			cut = append(cut, v)
		}
		out[k] = cut
	}
	return out
}
