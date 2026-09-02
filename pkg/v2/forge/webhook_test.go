package forge

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// vectorSecret is 0x00..0x1f, so the values below can be recomputed by anyone
// with a shell and no Go.
func vectorSecret() Secret {
	raw := make([]byte, SecretSize)
	for i := range raw {
		raw[i] = byte(i)
	}
	return SecretFrom(raw)
}

var (
	vectorBody = []byte(`{"ref":"refs/heads/main"}`)
	vectorAt   = time.Unix(1767225600, 0)
)

// TestExternalVectors asserts verify AND Sign against digests computed OUTSIDE
// this package (python hmac/base64 over the same inputs), so the two directions
// cannot be wrong in the same direction and still agree with each other. A
// round trip of Sign through verify would prove only that this file is
// self-consistent.
//
// They also PIN THE DERIVATION, which is a wire format: a forge stores the
// string Wire produced, so a change to how it is derived strands every hook
// registered before it, silently, at the next delivery. The python below is an
// independent HKDF written on top of hmac alone, so agreeing with it also
// checks the one convention that is easy to get wrong — RFC 5869's absent salt
// is HashLen zero bytes, not an empty string.
//
//	def hkdf(ikm, info):                        # RFC 5869, L = 32
//	    prk = hmac(key=b"\x00"*32, msg=ikm)      # Extract
//	    return hmac(key=prk, msg=info + b"\x01") # Expand, one block
//
//	dom       = "taubyte/forge/webhook/v1/"
//	mat(sc)   = hkdf(secret, dom + sc)
//	wire(hub) = urlsafe-b64-nopad(mat("hub-sha256"))
//	hub       = hex(hmac_sha256(key=wire(hub), msg=body))
//	wire(bit) = urlsafe-b64-nopad(mat("bitbucket-sha256"))
//	bit       = hex(hmac_sha256(key=wire(bit), msg=body))
//	wire(tok) = urlsafe-b64-nopad(mat("shared-token"))
//	wire(swh) = "whsec_" + b64(mat("standard-webhooks"))
//	swh       = b64(hmac_sha256(key=mat("standard-webhooks"),
//	                            msg="msg_1.1767225600."+body))
func TestExternalVectors(t *testing.T) {
	s := vectorSecret()

	const (
		hubWire = "smsN0Dxo7S3_xgA6eSALA2KuvoJ0Lel6q4spT1fXVNU"
		bitWire = "ftTZCqcUfbgi8yEbedq_urbDDlkbIKJ_e0QCaCfdkWU"
		tokWire = "XHAEMCpddVHFWlOi0cyi5JLDiCOFQjcyBaVT3D8_mE4"
		swhWire = "whsec_42kQbrrHtpg9EA+y4tmey46bHSqGj+0Obphd9eGI7Wg="
		hubMAC  = "sha256=2a129e3e78b14dfa508831af6f82fab659de665cb0387e94f83a8ef8c607c32f"
		bitMAC  = "sha256=7fd18b990b5e3a7c1ead981a0ab1081030c830d913c6c362fdbc9f98834e10e7"
		swhSig  = "v1,zFgJIyaQDIBwvg5Tiy2LHNtwfPE3v+PS5htEHIyELho="
	)

	// One wire string per scheme, and no two alike. These used to be three
	// copies of one value, which is what made a shared-token delivery hand out
	// a hub hook's HMAC key in cleartext.
	for want, sc := range map[string]Scheme{
		hubWire: SchemeHubSHA256,
		bitWire: SchemeBitbucketSHA256,
		tokWire: SchemeSharedToken,
		swhWire: SchemeStandardWebhooks,
	} {
		w, err := sc.Wire(s)
		require.NoError(t, err)
		assert.Equal(t, want, w, "%s wire", sc)
	}

	// And none of them is the Secret in an alphabet: the string a forge stores
	// is a one-way function of the secret, not an encoding of it.
	raw, err := base64.RawURLEncoding.DecodeString(hubWire)
	require.NoError(t, err)
	assert.NotEqual(t, s.Expose(), raw)

	t.Run("hub-sha256", func(t *testing.T) {
		d := Delivery{Header: http.Header{"X-Hub-Signature-256": {hubMAC}}, Body: vectorBody}
		require.NoError(t, verify(SchemeHubSHA256, s, d, vectorAt, time.Minute))

		got, err := Sign(SchemeHubSHA256, s, vectorBody, "", vectorAt)
		require.NoError(t, err)
		assert.Equal(t, hubMAC, got.Get("X-Hub-Signature-256"))
	})

	t.Run("bitbucket-puts-the-same-mac-in-githubs-sha1-header", func(t *testing.T) {
		// Same construction, different header — which is the whole reason it is
		// a scheme of its own and not an alias.
		d := Delivery{Header: http.Header{"X-Hub-Signature": {bitMAC}}, Body: vectorBody}
		require.NoError(t, verify(SchemeBitbucketSHA256, s, d, vectorAt, time.Minute))
		// And the SAME bytes in the OTHER header do not verify, which is what a
		// verifier dispatching on header names would get wrong.
		wrongHeader := Delivery{Header: http.Header{"X-Hub-Signature-256": {bitMAC}}, Body: vectorBody}
		require.Error(t, verify(SchemeBitbucketSHA256, s, wrongHeader, vectorAt, time.Minute))

		got, err := Sign(SchemeBitbucketSHA256, s, vectorBody, "", vectorAt)
		require.NoError(t, err)
		assert.Equal(t, bitMAC, got.Get("X-Hub-Signature"))
	})

	t.Run("standard-webhooks", func(t *testing.T) {
		hdr := http.Header{
			"Webhook-Id":        {"msg_1"},
			"Webhook-Timestamp": {"1767225600"},
			// Several space-separated signatures is how the standard rotates a
			// secret, and matching ANY of them is correct.
			"Webhook-Signature": {"v0,ignored " + swhSig},
		}
		d := Delivery{Header: hdr, Body: vectorBody}
		require.NoError(t, verify(SchemeStandardWebhooks, s, d, vectorAt, time.Minute))

		got, err := Sign(SchemeStandardWebhooks, s, vectorBody, "msg_1", vectorAt)
		require.NoError(t, err)
		assert.Equal(t, swhSig, got.Get("webhook-signature"))
		assert.Equal(t, "1767225600", got.Get("webhook-timestamp"))
	})

	t.Run("shared-token", func(t *testing.T) {
		d := Delivery{Header: http.Header{"X-Gitlab-Token": {tokWire}}, Body: vectorBody}
		require.NoError(t, verify(SchemeSharedToken, s, d, vectorAt, time.Minute))
		// It binds nothing: the same token over a different body still matches,
		// which is exactly why BindsBody is false and the zero Policy refuses it.
		other := Delivery{Header: d.Header, Body: []byte("anything at all")}
		require.NoError(t, verify(SchemeSharedToken, s, other, vectorAt, time.Minute))
		assert.False(t, SchemeSharedToken.BindsBody())

		got, err := Sign(SchemeSharedToken, s, vectorBody, "", vectorAt)
		require.NoError(t, err)
		assert.Equal(t, tokWire, got.Get("X-Gitlab-Token"))
		// THIS is the string a delivery hands the callback in cleartext, and
		// it is no longer any other scheme's HMAC key.
		hubKey, err := SchemeHubSHA256.key(s)
		require.NoError(t, err)
		assert.NotEqual(t, tokWire, string(hubKey))
	})
}

func TestVerifyRefusesEveryWrongDelivery(t *testing.T) {
	s := vectorSecret()
	const hubMAC = "sha256=2a129e3e78b14dfa508831af6f82fab659de665cb0387e94f83a8ef8c607c32f"

	hub := func(h http.Header, body []byte) error {
		return verify(SchemeHubSHA256, s, Delivery{Header: h, Body: body}, vectorAt, time.Minute)
	}

	for name, tc := range map[string]struct {
		header http.Header
		body   []byte
		reason string
	}{
		"no header at all": {
			header: http.Header{}, body: vectorBody, reason: "no signature header",
		},
		"a bare digest with no prefix": {
			// Gitea's own X-Gitea-Signature carries exactly this, which is why
			// the prefix is checked and not cut blindly.
			header: http.Header{"X-Hub-Signature-256": {strings.TrimPrefix(hubMAC, "sha256=")}},
			body:   vectorBody, reason: "not sha256-prefixed",
		},
		"a digest that is not hex": {
			header: http.Header{"X-Hub-Signature-256": {"sha256=zzzz"}},
			body:   vectorBody, reason: "not hex",
		},
		"a digest one character short": {
			header: http.Header{"X-Hub-Signature-256": {hubMAC[:len(hubMAC)-2]}},
			body:   vectorBody, reason: "does not match",
		},
		"a body with one byte appended": {
			header: http.Header{"X-Hub-Signature-256": {hubMAC}},
			body:   append(append([]byte(nil), vectorBody...), ' '), reason: "does not match",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := hub(tc.header, tc.body)
			var ve *VerificationError
			require.ErrorAs(t, err, &ve)
			assert.Contains(t, ve.Reason, tc.reason)
			// The error never carries the evidence.
			assert.NotContains(t, err.Error(), string(s.Expose()))
			assert.NotContains(t, err.Error(), hubMAC)
		})
	}
}

func TestStandardWebhooksBoundsItsReplayWindow(t *testing.T) {
	s := vectorSecret()
	signed, err := Sign(SchemeStandardWebhooks, s, vectorBody, "msg_1", vectorAt)
	require.NoError(t, err)
	d := Delivery{Header: signed, Body: vectorBody}

	require.NoError(t, verify(SchemeStandardWebhooks, s, d, vectorAt.Add(time.Minute), 5*time.Minute))
	require.NoError(t, verify(SchemeStandardWebhooks, s, d, vectorAt.Add(-time.Minute), 5*time.Minute))

	for name, now := range map[string]time.Time{
		"an hour late":  vectorAt.Add(time.Hour),
		"an hour early": vectorAt.Add(-time.Hour),
	} {
		t.Run(name, func(t *testing.T) {
			var ve *VerificationError
			require.ErrorAs(t, verify(SchemeStandardWebhooks, s, d, now, 5*time.Minute), &ve)
			assert.Contains(t, ve.Reason, "replay window")
		})
	}

	for name, hdr := range map[string]http.Header{
		"no id": {"Webhook-Timestamp": {"1767225600"}, "Webhook-Signature": {"v1,x"}},
		"a timestamp that is not a unix time": {
			"Webhook-Id": {"m"}, "Webhook-Timestamp": {"yesterday"}, "Webhook-Signature": {"v1,x"},
		},
		"a signature with no version": {
			"Webhook-Id": {"msg_1"}, "Webhook-Timestamp": {"1767225600"}, "Webhook-Signature": {"deadbeef"},
		},
		"a signature that is not base64": {
			"Webhook-Id": {"msg_1"}, "Webhook-Timestamp": {"1767225600"}, "Webhook-Signature": {"v1,!!!!"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, verify(SchemeStandardWebhooks, s, Delivery{Header: hdr, Body: vectorBody}, vectorAt, time.Minute))
		})
	}
}

func TestSharedTokenRefusesAnAbsentOrWrongToken(t *testing.T) {
	s := vectorSecret()
	require.Error(t, verify(SchemeSharedToken, s, Delivery{Header: http.Header{}}, vectorAt, time.Minute))
	require.Error(t, verify(SchemeSharedToken, s,
		Delivery{Header: http.Header{"X-Gitlab-Token": {"not-it"}}}, vectorAt, time.Minute))
}

func TestAnUnknownSchemeIsNeverUsable(t *testing.T) {
	s := vectorSecret()
	const made Scheme = "invented"

	assert.False(t, made.implemented())
	assert.False(t, made.BindsBody())
	assert.Empty(t, made.header())

	_, err := made.Wire(s)
	require.Error(t, err)
	_, err = made.key(s)
	require.Error(t, err)
	_, err = Sign(made, s, vectorBody, "", vectorAt)
	require.Error(t, err)
	require.Error(t, verify(made, s, Delivery{}, vectorAt, time.Minute))
}

func TestAnEmptySecretDerivesNothing(t *testing.T) {
	for _, sc := range []Scheme{SchemeHubSHA256, SchemeStandardWebhooks, SchemeSharedToken, SchemeBitbucketSHA256} {
		_, err := sc.Wire(Secret{})
		require.Error(t, err, "%s derived a wire secret from nothing", sc)
		_, err = Sign(sc, Secret{}, vectorBody, "id", vectorAt)
		require.Error(t, err, "%s signed with nothing", sc)
		require.Error(t, verify(sc, Secret{}, Delivery{
			Header: http.Header{
				"X-Hub-Signature-256": {"sha256=00"}, "X-Hub-Signature": {"sha256=00"},
				"X-Gitlab-Token": {"x"}, "Webhook-Id": {"m"},
				"Webhook-Timestamp": {"1767225600"}, "Webhook-Signature": {"v1,AA=="},
			},
			Body: vectorBody,
		}, vectorAt, time.Minute), "%s verified against nothing", sc)
	}
}

// TestEverySchemeInsistsOnAWholeSecret. The floor used to belong to one scheme
// and for the wrong reason: SchemeStandardWebhooks refused a short secret
// because GitLab's whsec_ FORMAT is base64 of exactly 32 bytes, so the other
// three keyed a MAC with whatever they were handed -- one byte, wire form
// "eA", exhaustible offline from a single captured delivery. The floor is a
// property of the SECRET now, so it holds for a scheme added tomorrow too.
func TestEverySchemeInsistsOnAWholeSecret(t *testing.T) {
	for _, sc := range []Scheme{SchemeHubSHA256, SchemeBitbucketSHA256, SchemeStandardWebhooks, SchemeSharedToken} {
		for _, short := range [][]byte{[]byte("x"), []byte("short"), make([]byte, SecretSize-1)} {
			weak := SecretFrom(short)
			_, err := sc.Wire(weak)
			require.Error(t, err, "%s derived a wire secret from %d bytes", sc, len(short))
			assert.Contains(t, err.Error(), "at least 32 bytes")

			// And nothing downstream of Wire is reachable around it.
			_, err = Sign(sc, weak, vectorBody, "m", vectorAt)
			require.Error(t, err, "%s signed with %d bytes", sc, len(short))
			require.Error(t, verify(sc, weak, Delivery{Header: http.Header{}, Body: vectorBody}, vectorAt, time.Minute))
		}
		_, err := sc.Wire(vectorSecret())
		require.NoError(t, err, "%s refused a whole secret", sc)
	}
}

func TestObjectIDsAreWholeOrNothing(t *testing.T) {
	assert.True(t, isObjectID("1f0c9d2b3a4e5f60718293a4b5c6d7e8f9012345"))
	assert.True(t, isObjectID(strings.Repeat("ab", 32)))
	assert.False(t, isObjectID(""))
	assert.False(t, isObjectID("1f0c9d2"), "an abbreviation that later matches two objects is a build of the wrong commit")
	assert.False(t, isObjectID(strings.Repeat("z", 40)))

	assert.True(t, isZeroOID(strings.Repeat("0", 40)))
	assert.True(t, isZeroOID(strings.Repeat("0", 64)))
	assert.False(t, isZeroOID(""))
	assert.False(t, isZeroOID("0000000000000000000000000000000000000001"))
}

// TestARefnameIsAGrammarAndNotABlocklist. The accepted half is the half that
// matters: a check that refuses the six shapes somebody thought of and refuses
// half the world with them is not a fix. Every refused case names the rule that
// refused it, so widening one rule cannot quietly widen another.
func TestARefnameIsAGrammarAndNotABlocklist(t *testing.T) {
	for _, ref := range []string{
		"refs/heads/main",
		"refs/heads/feature/JIRA-1234-a-long-but-entirely-ordinary-branch-name",
		"refs/tags/v1.2.3",
		"refs/tags/v1.2.3-rc.1+build.4",
		"refs/heads/dependabot/npm_and_yarn/packages/frontend/babel/plugin-transform-runtime-7.22.15",
		"refs/changes/12/3456/1",
		"refs/pull/17/head",
		"refs/heads/maître", // an accented branch name is a legal ref
		"refs/heads/主幹",     // and so is a CJK one
		"refs/heads/a.b.c",  // dots are fine anywhere but the places git names
		"refs/heads/locked", // only a whole ".lock" suffix is refused
		"refs/heads/" + strings.Repeat("a", maxRefComponent),
	} {
		assert.Empty(t, refReason(ref), "a legitimate ref was refused: %q", ref)
	}

	for name, ref := range map[string]string{
		"empty":                           "",
		"an option, not a ref":            "--upload-pack=/bin/sh",
		"a bare branch name":              "main",
		"a path climb":                    "../../../../etc/passwd",
		"a path climb inside refs":        "refs/heads/../../../etc/passwd",
		"two dots and nothing else wrong": "refs/heads/ma..in",
		"words":                           "just some words",
		"a newline":                       "refs/heads/main\nrefs/heads/evil",
		"a carriage return":               "refs/heads/main\rx",
		"a NUL":                           "refs/heads/main\x00",
		"a DEL":                           "refs/heads/main\x7f",
		"a tab":                           "refs/heads/ma\tin",
		"a space":                         "refs/heads/ma in",
		"a tilde":                         "refs/heads/main~1",
		"a caret":                         "refs/heads/main^2",
		"a colon":                         "refs/heads/main:refs/heads/evil",
		"a glob star":                     "refs/heads/*",
		"a glob question mark":            "refs/heads/mai?",
		"an open bracket":                 "refs/heads/mai[n]",
		"a backslash":                     "refs/heads/main\\evil",
		"a reflog selector":               "refs/heads/main@{1}",
		"a trailing dot":                  "refs/heads/main.",
		"a component starting with a dot": "refs/heads/.hidden",
		"a lock file":                     "refs/heads/main.lock",
		"a trailing slash":                "refs/heads/main/",
		"a doubled slash":                 "refs/heads//main",
		"refs and nothing else":           "refs/",
		"not under refs":                  "heads/main",
		"a megabyte of nothing":           "refs/heads/" + strings.Repeat("a", 1<<20),
		"one oversized component":         "refs/heads/" + strings.Repeat("a", maxRefComponent+1),
		"invalid utf-8":                   "refs/heads/ma\xffin",
	} {
		t.Run(name, func(t *testing.T) {
			reason := refReason(ref)
			require.NotEmpty(t, reason, "%q was accepted as a refname", ref)
			// The reason is a constant naming the rule. It never quotes the
			// ref, so a payload does not get to choose how long a log line is.
			assert.Less(t, len(reason), 80, "reason: %q", reason)
			if len(ref) >= 4 {
				assert.NotContains(t, reason, ref)
			}
		})
	}
}

// TestARefusedRefNamesTheRuleAndNotTheRef: a ref is a stranger's bytes, and
// VerifyPush's error is the thing that ends up in a log.
func TestARefusedRefNamesTheRuleAndNotTheRef(t *testing.T) {
	assert.Equal(t, "the ref is longer than a refname may be",
		refReason("refs/heads/"+strings.Repeat("a", MaxRefLength)))
	assert.Equal(t, "the delivery names no ref", refReason(""))
}

// TestOneSecretIsFourUnrelatedKeys is the domain separation stated as the thing
// that matters, over EVERY pair rather than the two a reviewer happened to try:
// nothing one scheme puts on the wire is anything another scheme would accept.
//
// The pairing that made it urgent is shared-token against hub: a shared-token
// delivery replays its wire string in a header, so anything that logs headers
// logs it, and that string used to BE the HMAC key of a hub hook holding the
// same Secret.
func TestOneSecretIsFourUnrelatedKeys(t *testing.T) {
	s := vectorSecret()
	all := []Scheme{SchemeHubSHA256, SchemeBitbucketSHA256, SchemeStandardWebhooks, SchemeSharedToken}

	seen := map[string]string{}
	note := func(value, what string) {
		if prior, ok := seen[value]; ok {
			t.Errorf("%s and %s are the same bytes", prior, what)
		}
		seen[value] = what
	}

	for _, sc := range all {
		wire, err := sc.Wire(s)
		require.NoError(t, err)
		note(wire, string(sc)+" wire")

		material, err := sc.material(s)
		require.NoError(t, err)
		require.Len(t, material, SecretSize, "%s material", sc)
		note(string(material), string(sc)+" material")

		// A MAC key is a scheme's wire bytes or its material, so it must not be
		// registered twice; what must hold is that it is never ANOTHER
		// scheme's, which the two notes above already establish.
		key, err := sc.key(s)
		if sc == SchemeSharedToken {
			require.Error(t, err, "a scheme that binds nothing has no MAC")
			continue
		}
		require.NoError(t, err)
		other := seen[string(key)]
		assert.Contains(t, other, string(sc), "%s keys with %s", sc, other)
	}

	// The Secret itself is never on the wire and never a key.
	note(string(s.Expose()), "the secret")
	assert.Len(t, seen, 2*len(all)+1)
}

// TestTheDerivationIsBoundToTheSchemeName. The label is what separates the
// domains, so a scheme renamed is a scheme whose hooks stop verifying — the
// same contract Scheme values already carry as stored wire values.
func TestTheDerivationIsBoundToTheSchemeName(t *testing.T) {
	s := vectorSecret()
	want, err := SchemeHubSHA256.material(s)
	require.NoError(t, err)

	got, err := hkdf.Key(sha256.New, s.Expose(), nil, secretDomain+string(SchemeHubSHA256), SecretSize)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	strayed, err := hkdf.Key(sha256.New, s.Expose(), nil, secretDomain+"hub-sha-256", SecretSize)
	require.NoError(t, err)
	assert.NotEqual(t, want, strayed, "a one-character difference in the label is a different key")
}
