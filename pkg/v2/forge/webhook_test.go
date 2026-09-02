package forge

import (
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
//	wire(hub) = urlsafe-b64-nopad(secret)
//	           = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"
//	hub       = hex(hmac_sha256(key=wire, msg=body))
//	wire(swh) = "whsec_" + b64(secret)
//	swh       = b64(hmac_sha256(key=secret, msg="msg_1.1767225600."+body))
func TestExternalVectors(t *testing.T) {
	s := vectorSecret()

	const (
		hubWire = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"
		swhWire = "whsec_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
		hubMAC  = "sha256=547153e250fc71985e64810186fd59498c88e9b038a9d0213435ac53587af97e"
		swhSig  = "v1,1wB09fVw3gLCvFU9lhmMkSjORo4YsCk9Yc3DMJphfVM="
	)

	for _, sc := range []Scheme{SchemeHubSHA256, SchemeBitbucketSHA256, SchemeSharedToken} {
		w, err := sc.Wire(s)
		require.NoError(t, err)
		assert.Equal(t, hubWire, w, "%s wire", sc)
	}
	w, err := SchemeStandardWebhooks.Wire(s)
	require.NoError(t, err)
	assert.Equal(t, swhWire, w)

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
		d := Delivery{Header: http.Header{"X-Hub-Signature": {hubMAC}}, Body: vectorBody}
		require.NoError(t, verify(SchemeBitbucketSHA256, s, d, vectorAt, time.Minute))
		// And the SAME bytes in the OTHER header do not verify, which is what a
		// verifier dispatching on header names would get wrong.
		wrongHeader := Delivery{Header: http.Header{"X-Hub-Signature-256": {hubMAC}}, Body: vectorBody}
		require.Error(t, verify(SchemeBitbucketSHA256, s, wrongHeader, vectorAt, time.Minute))

		got, err := Sign(SchemeBitbucketSHA256, s, vectorBody, "", vectorAt)
		require.NoError(t, err)
		assert.Equal(t, hubMAC, got.Get("X-Hub-Signature"))
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
		d := Delivery{Header: http.Header{"X-Gitlab-Token": {hubWire}}, Body: vectorBody}
		require.NoError(t, verify(SchemeSharedToken, s, d, vectorAt, time.Minute))
		// It binds nothing: the same token over a different body still matches,
		// which is exactly why BindsBody is false and the zero Policy refuses it.
		other := Delivery{Header: d.Header, Body: []byte("anything at all")}
		require.NoError(t, verify(SchemeSharedToken, s, other, vectorAt, time.Minute))
		assert.False(t, SchemeSharedToken.BindsBody())

		got, err := Sign(SchemeSharedToken, s, vectorBody, "", vectorAt)
		require.NoError(t, err)
		assert.Equal(t, hubWire, got.Get("X-Gitlab-Token"))
	})
}

func TestVerifyRefusesEveryWrongDelivery(t *testing.T) {
	s := vectorSecret()
	const hubMAC = "sha256=547153e250fc71985e64810186fd59498c88e9b038a9d0213435ac53587af97e"

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

// TestStandardWebhooksInsistsOnAWholeSecret: GitLab's own format check is
// whsec_ plus base64 of exactly 32 bytes, so a short secret must fail at
// derivation rather than register a hook the instance will reject.
func TestStandardWebhooksInsistsOnAWholeSecret(t *testing.T) {
	_, err := SchemeStandardWebhooks.Wire(SecretFrom([]byte("short")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "32-byte")
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
