package gogs_test

// One vector computed outside Go, in python3, so this file cannot agree with
// the implementation by computing the answer the same way:
//
//	import base64, hashlib, hmac
//
//	body   = b'{"ref":"refs/heads/main","after":"9b2c1d4e5f60718293a4b5c6d7e8f90123456789","repository":{"full_name":"acme/pay","clone_url":"https://gogs.example/acme/pay.git"}}'
//	secret = bytes(range(32))
//	info   = b"taubyte/forge/webhook/v1/" + b"gogs-sha256"   # secretDomain + the scheme
//
//	# HKDF-SHA256 (RFC 5869): extract under the all-zero salt, then one expand
//	# block, all of it since 32 bytes is SHA-256's output length.
//	prk    = hmac.new(b"\x00" * 32, secret, hashlib.sha256).digest()
//	okm    = hmac.new(prk, info + b"\x01", hashlib.sha256).digest()
//
//	wire   = base64.urlsafe_b64encode(okm).rstrip(b'=')
//	print(wire.decode())                                    # vectorWire
//	print(hmac.new(wire, body, hashlib.sha256).hexdigest())  # vectorSHA256
//
// Gogs sends the digest bare in X-Gogs-Signature, so GitHub's spelling of the
// same MAC is one of the rows that must be rejected.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/gogs"
	"github.com/taubyte/tau/pkg/git/repokey"
)

const (
	vectorBody = `{"ref":"refs/heads/main","after":"9b2c1d4e5f60718293a4b5c6d7e8f90123456789","repository":{"full_name":"acme/pay","clone_url":"https://gogs.example/acme/pay.git"}}`

	// vectorWire is what Gogs stores for this hook and keys its HMAC with:
	// base64url, unpadded, of HKDF-SHA256(vectorSecret, salt=none,
	// info="taubyte/forge/webhook/v1/"+"gogs-sha256", 32 bytes), computed from the
	// RFCs rather than from this package.
	vectorWire   = "5xhInBOJT_lftfUIGLTPSqJjOiVKH7IeRpq8qKCRD3Y"
	vectorSHA256 = "2065c47ac484c45f74f1b82254865bbecdd51b98adef602a64802bfa1ddf88f4"

	vectorCommit = "9b2c1d4e5f60718293a4b5c6d7e8f90123456789"
	vectorRef    = "refs/heads/main"
)

// vectorSecret is bytes 0x00..0x1f, the secret the vectors above were computed
// from.
func vectorSecret() forge.Secret {
	b := make([]byte, forge.SecretSize)
	for i := range b {
		b[i] = byte(i)
	}
	return forge.SecretFrom(b)
}

// instanceRegistry is a registry for gogs.example with no server behind it. Every
// test here goes through Registry.VerifyPush, which is the whole of a webhook
// receiver's forge surface: no credential, no session, no connection.
func instanceRegistry(t *testing.T) (*forge.Registry, forge.Hook) {
	t.Helper()
	drv, err := gogs.New("gogs.example", gogs.Transport(&canned{body: "{}"}))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: "gogs.example", Driver: drv})
	require.NoError(t, err)
	key, err := repokey.Parse("https://gogs.example/acme/pay.git")
	require.NoError(t, err)
	return reg, forge.Hook{
		Key:    key,
		Handle: "1",
		Scheme: forge.SchemeGogsSHA256,
		Secret: vectorSecret(),
	}
}

func pushHeader(sig string) http.Header {
	h := make(http.Header)
	h.Set("X-Gogs-Event", "push")
	h.Set("X-Gogs-Delivery", "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0")
	if sig != "" {
		h.Set("X-Gogs-Signature", sig)
	}
	return h
}

func TestTheVectorGogsWouldSendIsAccepted(t *testing.T) {
	reg, hook := instanceRegistry(t)
	push, err := reg.VerifyPush(hook, forge.Delivery{
		Header: pushHeader(vectorSHA256),
		Body:   []byte(vectorBody),
	})
	require.NoError(t, err)
	assert.Equal(t, forge.Push{Key: hook.Key, Ref: vectorRef, Commit: vectorCommit}, push)
}

func TestTheWireStringThisDriverHandsGogsIsTheVector(t *testing.T) {
	c := &canned{body: `{"id":7}`}
	drv, err := gogs.New("gogs.example", gogs.Transport(c))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: "gogs.example", Driver: drv})
	require.NoError(t, err)
	key, err := repokey.Parse("https://gogs.example/acme/pay.git")
	require.NoError(t, err)
	sess, err := reg.For(key, forge.Token("gh-tok-vector"))
	require.NoError(t, err)
	callback, err := url.Parse("https://build.example.invalid/hook")
	require.NoError(t, err)

	_, err = sess.RegisterPushHook(ctx(), key, callback, vectorSecret())
	require.NoError(t, err)
	require.Len(t, c.sent, 1)

	raw, err := c.sent[0].GetBody()
	require.NoError(t, err)
	type hookConfigBody struct {
		Secret string `json:"secret"`
	}
	type hookBody struct {
		Config hookConfigBody `json:"config"`
	}
	var body hookBody
	require.NoError(t, json.NewDecoder(raw).Decode(&body))

	assert.Equal(t, vectorWire, body.Config.Secret,
		"the string registered with the forge is not the one the vectors below are keyed with, so this hook would reject every delivery it ever received")
	assert.NotEqual(t, base64.RawURLEncoding.EncodeToString(vectorSecret().Expose()), body.Config.Secret,
		"the wire is an encoding of the Secret rather than a one-way function of it, which is the derivation this file's recipe was still printing long after it stopped being true")
}

func TestTheComparisonIsOverBytesAndNotOverTheHeaderString(t *testing.T) {
	reg, hook := instanceRegistry(t)
	push, err := reg.VerifyPush(hook, forge.Delivery{
		Header: pushHeader(strings.ToUpper(vectorSHA256)),
		Body:   []byte(vectorBody),
	})
	require.NoError(t, err)
	assert.Equal(t, vectorCommit, push.Commit)
}

func TestEveryWayASignatureCanBeWrongIsRejected(t *testing.T) {
	reg, hook := instanceRegistry(t)
	body := []byte(vectorBody)

	// A MAC over a body of the attacker's choosing, computed correctly, under a
	// key that is not the hook's — the hook's wire with its last byte changed,
	// so what is being rejected is the key and nothing else about the shape.
	otherKey := hmac.New(sha256.New, []byte(vectorWire[:len(vectorWire)-1]+"V"))
	otherKey.Write(body)
	underWrongKey := hex.EncodeToString(otherKey.Sum(nil))

	// The MAC of a DIFFERENT body, replayed onto this one. It is keyed with the
	// HOOK'S OWN wire, or this case would be rejected for the wrong key and
	// assert nothing about the body being bound.
	otherBody := hmac.New(sha256.New, []byte(vectorWire))
	otherBody.Write([]byte(`{"ref":"refs/heads/main"}`))
	ofAnotherBody := hex.EncodeToString(otherBody.Sum(nil))

	for name, d := range map[string]forge.Delivery{
		"no signature header at all": {
			Header: pushHeader(""), Body: body,
		},
		"the digest with GitHub's sha256= prefix, which Gogs never sends": {
			Header: pushHeader("sha256=" + vectorSHA256), Body: body,
		},
		"the right digest in GitHub's header and not in Gogs's": {
			Header: func() http.Header {
				h := pushHeader("")
				h.Set("X-Hub-Signature-256", "sha256="+vectorSHA256)
				return h
			}(), Body: body,
		},
		"a digest that is not hex": {
			Header: pushHeader("zzzz" + vectorSHA256[4:]), Body: body,
		},
		"a digest truncated by one character": {
			Header: pushHeader(vectorSHA256[:len(vectorSHA256)-1]), Body: body,
		},
		"a digest of the right length that is all zeros": {
			Header: pushHeader(strings.Repeat("0", 64)), Body: body,
		},
		"a correct MAC under a different key": {
			Header: pushHeader(underWrongKey), Body: body,
		},
		"a correct MAC of a different body": {
			Header: pushHeader(ofAnotherBody), Body: body,
		},
		"the body with one byte flipped": {
			Header: pushHeader(vectorSHA256),
			Body:   []byte(strings.Replace(vectorBody, "refs/heads/main", "refs/heads/mair", 1)),
		},
		"the body with one byte appended": {
			Header: pushHeader(vectorSHA256), Body: append(append([]byte(nil), body...), ' '),
		},
		"the body with one byte removed": {
			Header: pushHeader(vectorSHA256), Body: body[:len(body)-1],
		},
		"an empty body": {
			Header: pushHeader(vectorSHA256), Body: nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := reg.VerifyPush(hook, d)
			require.Error(t, err, "a forged or corrupted delivery was accepted")
			assert.Equal(t, forge.Push{}, got, "a rejected delivery produced a Push beside its error")
		})
	}
}

func TestAHookRecordedUnderAnotherSchemeDoesNotVerify(t *testing.T) {
	reg, hook := instanceRegistry(t)
	for _, s := range []forge.Scheme{forge.SchemeHubSHA256, forge.SchemeBitbucketSHA256, forge.SchemeStandardWebhooks} {
		other := hook
		other.Scheme = s
		_, err := reg.VerifyPush(other, forge.Delivery{
			Header: pushHeader(vectorSHA256), Body: []byte(vectorBody),
		})
		require.Error(t, err, "a delivery verified under %s", s)
	}
}

func TestWhatAVerifiedPayloadIsReadFor(t *testing.T) {
	reg, hook := instanceRegistry(t)

	signed := func(t *testing.T, body string, event string) (forge.Push, error) {
		t.Helper()
		hdr, err := forge.Sign(hook.Scheme, hook.Secret, []byte(body), "d1", time.Now())
		require.NoError(t, err)
		hdr.Set("X-Gogs-Event", event)
		return reg.VerifyPush(hook, forge.Delivery{Header: hdr, Body: []byte(body)})
	}

	t.Run("a delivery that is not a push", func(t *testing.T) {
		_, err := signed(t, `{}`, "ping")
		require.ErrorIs(t, err, forge.ErrNotAPush)
	})

	t.Run("an event name in another case is still a push", func(t *testing.T) {
		_, err := signed(t, vectorBody, "Push")
		require.NoError(t, err)
	})

	t.Run("a payload that is not json", func(t *testing.T) {
		_, err := signed(t, `{ this is not json`, "push")
		require.Error(t, err)
	})

	t.Run("a payload naming no repository", func(t *testing.T) {
		_, err := signed(t, `{"ref":"refs/heads/main","after":"`+vectorCommit+`"}`, "push")
		require.Error(t, err)
	})

	t.Run("a payload naming a repository by an implausible clone URL", func(t *testing.T) {
		_, err := signed(t, `{"ref":"x","repository":{"clone_url":"https://gogs.example/acme/`+strings.Repeat("a", 600)+`"}}`, "push")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), strings.Repeat("a", 600), "a payload chose how long a log line would be")
	})

	t.Run("a clone URL this layer cannot name", func(t *testing.T) {
		_, err := signed(t, `{"ref":"x","after":"`+vectorCommit+`","repository":{"clone_url":"telnet://nope"}}`, "push")
		require.Error(t, err)
	})

	t.Run("a push naming no commit at all", func(t *testing.T) {
		_, err := signed(t, `{"ref":"`+vectorRef+`","repository":{"clone_url":"https://gogs.example/acme/pay.git"}}`, "push")
		require.Error(t, err)
	})

	// Gogs reports a branch deletion as a "delete" event, not this; a push that
	// does name the zero id still reaches the core, which refuses to build it.
	t.Run("a ref deletion is a legitimate delivery with nothing to build", func(t *testing.T) {
		_, err := signed(t, `{"ref":"`+vectorRef+`","after":"`+strings.Repeat("0", 40)+`","repository":{"clone_url":"https://gogs.example/acme/pay.git"}}`, "push")
		require.ErrorIs(t, err, forge.ErrRefDeleted)
	})

	t.Run("an abbreviated commit is not an object id", func(t *testing.T) {
		_, err := signed(t, `{"ref":"`+vectorRef+`","after":"9b2c1d4","repository":{"clone_url":"https://gogs.example/acme/pay.git"}}`, "push")
		require.Error(t, err, "an abbreviation that later matches two objects is a build of the wrong commit")
	})

	t.Run("a hook may only speak for its own repository", func(t *testing.T) {
		_, err := signed(t, `{"ref":"`+vectorRef+`","after":"`+vectorCommit+`","repository":{"clone_url":"https://github.com/somebody/else.git"}}`, "push")
		var ve *forge.VerificationError
		require.ErrorAs(t, err, &ve)
		assert.Contains(t, ve.Reason, "claims repository")
	})
}
