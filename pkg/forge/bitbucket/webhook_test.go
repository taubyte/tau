package bitbucket_test

// A SIGNATURE CHECK THAT IS ONLY TESTED ON ITS HAPPY PATH IS NOT TESTED. What
// follows is one vector computed OUTSIDE Go — python's hmac, hashlib and base64
// — and then every way a delivery can be wrong, and then every way a verified
// Bitbucket payload can say something this driver must not read as a push.
//
// The recipe, verbatim, to be pasted into python3:
//
//	import base64, hashlib, hmac
//
//	body   = b'{"push":{"changes":[{"new":{"type":"branch","name":"main","target":{"hash":"9b2c1d4e5f60718293a4b5c6d7e8f90123456789"}},"old":null}]},"repository":{"full_name":"acme/pay","links":{"html":{"href":"https://bitbucket.org/acme/pay"}}}}'
//	secret = bytes(range(32))
//	info   = b"taubyte/forge/webhook/v1/" + b"bitbucket-sha256"   # secretDomain + the scheme
//
//	prk    = hmac.new(b"\x00" * 32, secret, hashlib.sha256).digest()   # HKDF extract, RFC 5869
//	okm    = hmac.new(prk, info + b"\x01", hashlib.sha256).digest()    # one expand block
//
//	wire   = base64.urlsafe_b64encode(okm).rstrip(b'=')
//	print(wire.decode())                                    # vectorWire
//	print(hmac.new(wire, body, hashlib.sha256).hexdigest())  # vectorSHA256
//	print(hmac.new(wire, body, hashlib.sha1).hexdigest())    # vectorSHA1
//
// Bitbucket Cloud sends "X-Hub-Signature: sha256=<hex>" — GitHub's
// construction in GitHub's SHA-1 header name — which is why the scheme is its
// own and not SchemeHubSHA256. The SHA-1 vector is here to be REJECTED.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/bitbucket"
	"github.com/taubyte/tau/pkg/git/repokey"
)

const (
	vectorBody   = `{"push":{"changes":[{"new":{"type":"branch","name":"main","target":{"hash":"9b2c1d4e5f60718293a4b5c6d7e8f90123456789"}},"old":null}]},"repository":{"full_name":"acme/pay","links":{"html":{"href":"https://bitbucket.org/acme/pay"}}}}`
	vectorWire   = "ftTZCqcUfbgi8yEbedq_urbDDlkbIKJ_e0QCaCfdkWU"
	vectorSHA256 = "9e3f28274270841ea1b03fed6150cdc74342dc130984a0aa218d9036351d8956"
	vectorSHA1   = "57f62c88600f88ac602178a348eb1cd31701cfdb"

	vectorCommit = "9b2c1d4e5f60718293a4b5c6d7e8f90123456789"
	vectorRef    = "refs/heads/main"
)

func vectorSecret() forge.Secret {
	b := make([]byte, forge.SecretSize)
	for i := range b {
		b[i] = byte(i)
	}
	return forge.SecretFrom(b)
}

// cloudRegistry is a registry for bitbucket.org with no server behind it:
// Registry.VerifyPush is the whole of a webhook receiver's forge surface.
func cloudRegistry(t *testing.T) (*forge.Registry, forge.Hook) {
	t.Helper()
	drv, err := bitbucket.New("bitbucket.org")
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: "bitbucket.org", Driver: drv})
	require.NoError(t, err)
	key, err := repokey.Parse("https://bitbucket.org/acme/pay.git")
	require.NoError(t, err)
	return reg, forge.Hook{Key: key, Handle: "{1}", Scheme: forge.SchemeBitbucketSHA256, Secret: vectorSecret()}
}

func pushHeader(sig string) http.Header {
	h := make(http.Header)
	h.Set("X-Event-Key", "repo:push")
	h.Set("X-Request-UUID", "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0")
	if sig != "" {
		h.Set("X-Hub-Signature", sig)
	}
	return h
}

// TestTheVectorBitbucketWouldSendIsAccepted. If this fails, no delivery from
// Bitbucket Cloud would ever verify.
func TestTheVectorBitbucketWouldSendIsAccepted(t *testing.T) {
	reg, hook := cloudRegistry(t)
	push, err := reg.VerifyPush(hook, forge.Delivery{Header: pushHeader("sha256=" + vectorSHA256), Body: []byte(vectorBody)})
	require.NoError(t, err)
	assert.Equal(t, forge.Push{Key: hook.Key, Ref: vectorRef, Commit: vectorCommit}, push)
}

// TestTheWireStringThisDriverHandsBitbucketIsTheVector: the secret this driver
// registers a hook with is the one the vector's HMAC is keyed by.
func TestTheWireStringThisDriverHandsBitbucketIsTheVector(t *testing.T) {
	drv, err := bitbucket.New("bitbucket.org")
	require.NoError(t, err)
	sig, ok := drv.(forge.Signatures)
	require.True(t, ok)
	require.Equal(t, forge.SchemeBitbucketSHA256, sig.Scheme())

	wire, err := sig.Scheme().Wire(vectorSecret())
	require.NoError(t, err)
	assert.Equal(t, vectorWire, wire)
}

func TestEveryWayASignatureCanBeWrongIsRejected(t *testing.T) {
	reg, hook := cloudRegistry(t)
	for name, hdr := range map[string]http.Header{
		"no signature":                  pushHeader(""),
		"the SHA-1 digest":              pushHeader("sha1=" + vectorSHA1),
		"the SHA-1 digest under sha256": pushHeader("sha256=" + vectorSHA1),
		"a digest without its prefix":   pushHeader(vectorSHA256),
		"one hex digit wrong":           pushHeader("sha256=" + vectorSHA256[:63] + "0"),
		"the right digest in GitHub's header": func() http.Header {
			h := pushHeader("")
			h.Set("X-Hub-Signature-256", "sha256="+vectorSHA256)
			return h
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := reg.VerifyPush(hook, forge.Delivery{Header: hdr, Body: []byte(vectorBody)})
			require.Error(t, err)
		})
	}

	t.Run("a body one byte different", func(t *testing.T) {
		body := strings.Replace(vectorBody, "main", "mainx", 1)
		_, err := reg.VerifyPush(hook, forge.Delivery{Header: pushHeader("sha256=" + vectorSHA256), Body: []byte(body)})
		require.Error(t, err)
	})
}

// TestWhatAVerifiedPayloadIsReadFor. Everything below is a correctly signed
// delivery, so what is asserted is the reading and not the checking.
func TestWhatAVerifiedPayloadIsReadFor(t *testing.T) {
	reg, hook := cloudRegistry(t)

	signed := func(t *testing.T, body, event string) (forge.Push, error) {
		t.Helper()
		hdr, err := forge.Sign(hook.Scheme, hook.Secret, []byte(body), "d1", time.Now())
		require.NoError(t, err)
		hdr.Set("X-Event-Key", event)
		return reg.VerifyPush(hook, forge.Delivery{Header: hdr, Body: []byte(body)})
	}
	repo := `"repository":{"links":{"html":{"href":"https://bitbucket.org/acme/pay"}}}`
	side := func(kind, name, hash string) string {
		return `{"type":"` + kind + `","name":"` + name + `","target":{"hash":"` + hash + `"}}`
	}
	changes := func(c ...string) string { return `"push":{"changes":[` + strings.Join(c, ",") + `]}` }
	push := func(parts ...string) string { return "{" + strings.Join(parts, ",") + "}" }

	t.Run("a delivery that is not a push", func(t *testing.T) {
		_, err := signed(t, vectorBody, "repo:commit_status_created")
		require.ErrorIs(t, err, forge.ErrNotAPush)
	})

	t.Run("a payload that is not json", func(t *testing.T) {
		_, err := signed(t, `{ not json`, "repo:push")
		require.Error(t, err)
	})

	t.Run("a payload naming no repository", func(t *testing.T) {
		_, err := signed(t, push(changes(`{"new":`+side("branch", "main", vectorCommit)+`}`)), "repo:push")
		require.Error(t, err)
	})

	t.Run("a repository named by an implausible link", func(t *testing.T) {
		long := strings.Repeat("a", 600)
		_, err := signed(t, `{"repository":{"links":{"html":{"href":"https://bitbucket.org/acme/`+long+`"}}}}`, "repo:push")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), long)
	})

	t.Run("a link this layer cannot name, carrying a password", func(t *testing.T) {
		_, err := signed(t, `{"repository":{"links":{"html":{"href":"https://u:hunter2-secret@bitbucket.org/acme/pay"}}}}`, "repo:push")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "hunter2-secret", "a payload put a credential into a tau log")
	})

	t.Run("no ref change at all", func(t *testing.T) {
		_, err := signed(t, push(repo, changes()), "repo:push")
		require.Error(t, err)
	})

	t.Run("two ref changes in one delivery are refused, not half-read", func(t *testing.T) {
		a := `{"new":` + side("branch", "main", vectorCommit) + `}`
		b := `{"new":` + side("branch", "dev", vectorCommit) + `}`
		_, err := signed(t, push(repo, changes(a, b)), "repo:push")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "2 ref changes")
	})

	t.Run("a change with neither side", func(t *testing.T) {
		_, err := signed(t, push(repo, changes(`{"new":null,"old":null}`)), "repo:push")
		require.Error(t, err)
	})

	t.Run("a tag", func(t *testing.T) {
		got, err := signed(t, push(repo, changes(`{"new":`+side("tag", "v1.0.0", vectorCommit)+`}`)), "repo:push")
		require.NoError(t, err)
		assert.Equal(t, forge.Push{Key: hook.Key, Ref: "refs/tags/v1.0.0", Commit: vectorCommit}, got)
	})

	t.Run("a deletion is a legitimate delivery with nothing to build", func(t *testing.T) {
		_, err := signed(t, push(repo, changes(`{"new":null,"old":`+side("branch", "main", vectorCommit)+`}`)), "repo:push")
		require.ErrorIs(t, err, forge.ErrRefDeleted)
	})

	t.Run("a ref that is neither a branch nor a tag", func(t *testing.T) {
		_, err := signed(t, push(repo, changes(`{"new":`+side("bookmark", "main", vectorCommit)+`}`)), "repo:push")
		require.Error(t, err)
	})

	t.Run("a ref with no name", func(t *testing.T) {
		_, err := signed(t, push(repo, changes(`{"new":`+side("branch", "", vectorCommit)+`}`)), "repo:push")
		require.Error(t, err)
	})

	t.Run("an abbreviated commit is not an object id", func(t *testing.T) {
		_, err := signed(t, push(repo, changes(`{"new":`+side("branch", "main", "9b2c1d4")+`}`)), "repo:push")
		require.Error(t, err)
	})

	t.Run("a hook may only speak for its own repository", func(t *testing.T) {
		other := `"repository":{"links":{"html":{"href":"https://bitbucket.org/somebody/else"}}}`
		_, err := signed(t, push(other, changes(`{"new":`+side("branch", "main", vectorCommit)+`}`)), "repo:push")
		var ve *forge.VerificationError
		require.ErrorAs(t, err, &ve)
	})
}
