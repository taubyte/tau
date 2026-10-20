package forge

import (
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// The SchemePayloadEd25519 vectors were computed OUTSIDE this package, with
// python's hmac and PyNaCl, over the inputs below:
//
//	mat  = hkdf(secret=0x00..0x1f, "taubyte/forge/webhook/v1/payload-ed25519")
//	wire = "tau_" + hex(mat)
//	sk   = nacl.signing.SigningKey(seed=0x20..0x3f)
//	sig  = b64(sk.sign(body + nonce).signature)
//
// sourcehut publishes its production public key but no signed example, so the
// key pair here is generated; what the vector pins is the construction the
// sourcehut documentation states — "ed25519 signature of the request body
// concatenated with the X-Payload-Nonce header".
const (
	payloadWire  = "tau_5537e67d23b9b2e400fcd9fbaeb915310c35cb8bc6bd3fc96768ed5a5fabb829"
	payloadPub   = "Kay64UG8yvCyLhqU000LxzYeUm0L/hLIl5S8kyKWbdc="
	payloadNonce = "8d4c1b0e"
	payloadSig   = "z3oTFp5xjyRp46hMpj4+SKWRMeDFt9oWeU/hHlmx4sun6/7mBatiUjMGaHpmttqTWdykLOqC8FDeGVaO6Dj/DQ=="
	payloadBody  = `{"data":{"webhook":{"event":"GIT_POST_RECEIVE"},"` + payloadWire + `":"Query"}}`
)

func payloadKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(0x20 + i)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	require.Equal(t, payloadPub, base64.StdEncoding.EncodeToString(pub))
	return pub, priv
}

func TestPayloadEd25519ExternalVectors(t *testing.T) {
	pub, priv := payloadKeyPair(t)
	s := vectorSecret()

	wire, err := SchemePayloadEd25519.Wire(s)
	require.NoError(t, err)
	assert.Equal(t, payloadWire, wire)

	hdr := SignPayload(priv, []byte(payloadBody), payloadNonce)
	assert.Equal(t, payloadSig, hdr.Get("X-Payload-Signature"))
	assert.Equal(t, payloadNonce, hdr.Get("X-Payload-Nonce"))

	d := Delivery{Header: http.Header{}, Body: []byte(payloadBody)}
	d.Header.Set("X-Payload-Signature", payloadSig)
	d.Header.Set("X-Payload-Nonce", payloadNonce)
	require.NoError(t, verifyWith(SchemePayloadEd25519, s, d, time.Now(), DefaultTimestampTolerance, pub))

	_, err = Sign(SchemePayloadEd25519, s, []byte(payloadBody), "", time.Now())
	assert.Error(t, err, "a forge-keyed scheme has no secret-keyed signer")
	_, err = SchemePayloadEd25519.key(s)
	assert.Error(t, err, "a forge-keyed scheme has no MAC key")
}

func TestPayloadEd25519RefusesEveryWrongDelivery(t *testing.T) {
	pub, priv := payloadKeyPair(t)
	otherPub, otherPriv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	s := vectorSecret()

	signed := func(body, nonce string, key ed25519.PrivateKey) Delivery {
		return Delivery{Header: SignPayload(key, []byte(body), nonce), Body: []byte(body)}
	}
	good := signed(payloadBody, payloadNonce, priv)

	for name, tc := range map[string]struct {
		d   Delivery
		key ed25519.PublicKey
	}{
		"a body one byte different": {d: Delivery{Header: good.Header, Body: []byte(strings.Replace(payloadBody, "GIT_", "GIT-", 1))}, key: pub},
		"another nonce":             {d: Delivery{Header: http.Header{"X-Payload-Signature": good.Header["X-Payload-Signature"], "X-Payload-Nonce": {"other"}}, Body: good.Body}, key: pub},
		"no nonce":                  {d: Delivery{Header: http.Header{"X-Payload-Signature": good.Header["X-Payload-Signature"]}, Body: good.Body}, key: pub},
		"no signature":              {d: Delivery{Header: http.Header{"X-Payload-Nonce": {payloadNonce}}, Body: good.Body}, key: pub},
		"a nonce too long":          {d: signed(payloadBody, strings.Repeat("n", maxPayloadNonce+1), priv), key: pub},
		"a signature not base64":    {d: Delivery{Header: http.Header{"X-Payload-Signature": {"!!"}, "X-Payload-Nonce": {payloadNonce}}, Body: good.Body}, key: pub},
		"a signature too short":     {d: Delivery{Header: http.Header{"X-Payload-Signature": {"AAAA"}, "X-Payload-Nonce": {payloadNonce}}, Body: good.Body}, key: pub},
		"another forge's key":       {d: signed(payloadBody, payloadNonce, otherPriv), key: pub},
		"the driver names no key":   {d: good, key: nil},
		"signed but not json":       {d: signed("not json", payloadNonce, priv), key: pub},
		// The confused deputy this scheme exists for: the forge really signed
		// it, for a subscription that is not this hook's.
		"another subscription's delivery": {d: signed(`{"data":{"webhook":{"event":"GIT_POST_RECEIVE"},"tau_00":"Query"}}`, payloadNonce, priv), key: pub},
		"the alias holding another value": {d: signed(`{"data":{"`+payloadWire+`":"Mutation"}}`, payloadNonce, priv), key: pub},
	} {
		var ve *VerificationError
		assert.ErrorAs(t, verifyWith(SchemePayloadEd25519, s, tc.d, time.Now(), DefaultTimestampTolerance, tc.key), &ve, name)
	}
	assert.NotEqual(t, pub, otherPub)
}

// keyedStub is a stub that names the key its forge signs with.
type keyedStub struct {
	*stub
	key ed25519.PublicKey
}

func (k *keyedStub) PayloadKey() ed25519.PublicKey { return k.key }

func TestPayloadEd25519NeedsTheForgesKeyAtNewAndAtVerify(t *testing.T) {
	pub, priv := payloadKeyPair(t)

	_, err := New(Policy{}, Binding{Host: stubHost, Driver: &stub{host: stubHost, scheme: SchemePayloadEd25519}})
	assert.ErrorContains(t, err, "names no Ed25519 key")
	_, err = New(Policy{}, Binding{Host: stubHost, Driver: &keyedStub{stub: &stub{host: stubHost, scheme: SchemePayloadEd25519}, key: pub[:3]}})
	assert.ErrorContains(t, err, "names no Ed25519 key")

	key := repokey.Key{Host: stubHost, Owner: "~acme", Name: "pay"}
	const commit = "1f0c9d2b3a4e5f60718293a4b5c6d7e8f9012345"
	ks := &keyedStub{stub: &stub{host: stubHost, scheme: SchemePayloadEd25519, claim: PushClaim{Repository: key, Ref: "refs/heads/main", Commit: commit}}, key: pub}
	reg, err := New(Policy{}, Binding{Host: stubHost, Driver: ks})
	require.NoError(t, err)

	h := Hook{Key: key, Handle: "7", Scheme: SchemePayloadEd25519, Secret: vectorSecret()}
	d := Delivery{Header: SignPayload(priv, []byte(payloadBody), payloadNonce), Body: []byte(payloadBody)}
	push, err := reg.VerifyPush(h, d)
	require.NoError(t, err)
	assert.Equal(t, Push{Key: key, Ref: "refs/heads/main", Commit: commit}, push)

	// A registry built before the driver lost its key face cannot happen, but
	// a stored hook naming this scheme on a driver without the face can.
	plain, err := New(Policy{}, Binding{Host: stubHost, Driver: &stub{host: stubHost, scheme: SchemeHubSHA256, claim: ks.claim}})
	require.NoError(t, err)
	_, err = plain.VerifyPush(h, d)
	assert.ErrorContains(t, err, "names no key")
}
