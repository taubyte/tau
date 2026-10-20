package forge

// ADVERSARIAL. Attempts to defeat the verification path, to make a secret
// render, and to aim a request at a host nobody bound.

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
)

var advSecret = SecretFrom([]byte("0123456789abcdef0123456789abcdef"))

const advBody = `{"ref":"refs/heads/main"}`

func advHubMAC(t *testing.T, body string) string {
	t.Helper()
	k, err := SchemeHubSHA256.key(advSecret)
	require.NoError(t, err)
	m := hmac.New(sha256.New, k)
	m.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// TestAdvNoForgeryWithoutTheSecret is every shape of a signature an attacker
// who does not hold the secret can produce, plus the ones an attacker who holds
// a signature over OTHER bytes can produce.
func TestAdvNoForgeryWithoutTheSecret(t *testing.T) {
	good := advHubMAC(t, advBody)
	otherBody := advHubMAC(t, "{}")

	sha1MAC := func() string {
		k, err := SchemeHubSHA256.key(advSecret)
		require.NoError(t, err)
		m := hmac.New(sha1.New, k)
		m.Write([]byte(advBody))
		return "sha1=" + hex.EncodeToString(m.Sum(nil))
	}()

	for name, sig := range map[string]string{
		"an empty header value":                       "",
		"the prefix and nothing else":                 "sha256=",
		"the prefix and a right-length wrong digest":  "sha256=" + strings.Repeat("00", sha256.Size),
		"a valid MAC over different bytes":            otherBody,
		"a digest truncated to half":                  good[:len("sha256=")+32],
		"a digest with a byte appended":               good + "00",
		"an uppercase scheme prefix":                  "SHA256=" + strings.TrimPrefix(good, "sha256="),
		"the sha1 sibling, correctly keyed":           sha1MAC,
		"a bare digest with no algorithm named":       strings.TrimPrefix(good, "sha256="),
		"the standard-webhooks spelling in this slot": "v1," + base64.StdEncoding.EncodeToString([]byte("x")),
		"whitespace around a valid digest":            " " + good + " ",
		"a base64 digest instead of hex":              "sha256=" + base64.StdEncoding.EncodeToString([]byte("x")),
	} {
		t.Run(name, func(t *testing.T) {
			d := Delivery{Header: http.Header{"X-Hub-Signature-256": {sig}}, Body: []byte(advBody)}
			err := verify(SchemeHubSHA256, advSecret, d, time.Now(), time.Minute)
			require.Error(t, err, "a delivery signed %q was accepted", sig)
			assert.NotContains(t, err.Error(), string(advSecret.Expose()))
		})
	}

	t.Run("no header at all", func(t *testing.T) {
		require.Error(t, verify(SchemeHubSHA256, advSecret,
			Delivery{Header: http.Header{}, Body: []byte(advBody)}, time.Now(), time.Minute))
	})

	t.Run("the wrong scheme's header carrying a valid MAC", func(t *testing.T) {
		// A valid X-Hub-Signature-256 presented where SchemeBitbucketSHA256
		// reads X-Hub-Signature must not verify: the scheme decides the header.
		d := Delivery{Header: http.Header{"X-Hub-Signature-256": {good}}, Body: []byte(advBody)}
		require.Error(t, verify(SchemeBitbucketSHA256, advSecret, d, time.Now(), time.Minute))
	})

	t.Run("a second bogus value first in a repeated header", func(t *testing.T) {
		d := Delivery{
			Header: http.Header{"X-Hub-Signature-256": {"sha256=" + strings.Repeat("aa", 32), good}},
			Body:   []byte(advBody),
		}
		require.Error(t, verify(SchemeHubSHA256, advSecret, d, time.Now(), time.Minute),
			"Header.Get reads the first value; a delivery must not pass on a later one")
	})

	t.Run("the header case does not matter and the secret still does", func(t *testing.T) {
		h := http.Header{}
		h.Set("x-hub-signature-256", good)
		require.NoError(t, verify(SchemeHubSHA256, advSecret,
			Delivery{Header: h, Body: []byte(advBody)}, time.Now(), time.Minute))
		require.Error(t, verify(SchemeHubSHA256, SecretFrom([]byte("another secret entirely")),
			Delivery{Header: h, Body: []byte(advBody)}, time.Now(), time.Minute))
	})

	t.Run("an empty secret verifies nothing, not everything", func(t *testing.T) {
		for _, sc := range []Scheme{SchemeHubSHA256, SchemeBitbucketSHA256, SchemeStandardWebhooks, SchemeSharedToken, SchemeOneDevToken} {
			h := http.Header{}
			h.Set("X-Hub-Signature-256", "sha256=")
			h.Set("X-Hub-Signature", "sha256=")
			h.Set("X-Gitlab-Token", "")
			h.Set("X-OneDev-Signature", "")
			h.Set("Webhook-Id", "m")
			h.Set("Webhook-Timestamp", fmt.Sprint(time.Now().Unix()))
			h.Set("Webhook-Signature", "v1,")
			require.Error(t, verify(sc, Secret{}, Delivery{Header: h, Body: nil}, time.Now(), time.Minute),
				"%s verified a delivery against no secret at all", sc)
		}
	})

	t.Run("standard webhooks: a candidate list of near misses", func(t *testing.T) {
		now := time.Now()
		signed, err := Sign(SchemeStandardWebhooks, advSecret, []byte(advBody), "msg_1", now)
		require.NoError(t, err)
		real := signed.Get("webhook-signature")

		for name, sig := range map[string]string{
			"the version stripped":      strings.TrimPrefix(real, "v1,"),
			"another version named":     "v2," + strings.TrimPrefix(real, "v1,"),
			"an empty candidate":        "v1,",
			"a hundred empty guesses":   strings.TrimSpace(strings.Repeat("v1,AAAA ", 100)),
			"the id changed under it":   real,
			"the body changed under it": real,
		} {
			t.Run(name, func(t *testing.T) {
				h := http.Header{}
				h.Set("webhook-id", "msg_1")
				h.Set("webhook-timestamp", fmt.Sprint(now.Unix()))
				h.Set("webhook-signature", sig)
				body := []byte(advBody)
				switch name {
				case "the id changed under it":
					h.Set("webhook-id", "msg_2")
				case "the body changed under it":
					body = []byte(advBody + " ")
				}
				require.Error(t, verify(SchemeStandardWebhooks, advSecret,
					Delivery{Header: h, Body: body}, now, time.Minute))
			})
		}
	})

	t.Run("shared token: near misses", func(t *testing.T) {
		wire, err := SchemeSharedToken.Wire(advSecret)
		require.NoError(t, err)
		for name, presented := range map[string]string{
			"empty":          "",
			"a prefix":       wire[:len(wire)-1],
			"a suffix added": wire + "x",
			"padded":         " " + wire,
			"case folded":    strings.ToUpper(wire),
		} {
			t.Run(name, func(t *testing.T) {
				require.Error(t, verify(SchemeSharedToken, advSecret,
					Delivery{Header: http.Header{"X-Gitlab-Token": {presented}}, Body: []byte(advBody)},
					time.Now(), time.Minute))
			})
		}
	})
}

// TestAdvTheSharedTokenWireIsExactlyTheHubSchemesHMACKey.
//
// Scheme.Wire derives the same string for SchemeHubSHA256, SchemeBitbucketSHA256
// and SchemeSharedToken, and Scheme.key for the two hub schemes is THAT STRING'S
// BYTES. There is no domain separation anywhere in the derivation, so one Secret
// used for hooks under two schemes is one key used twice — and SchemeSharedToken
// hands that key to the callback in cleartext on every single delivery.
func TestAdvTheSharedTokenWireIsExactlyTheHubSchemesHMACKey(t *testing.T) {
	shared, err := SchemeSharedToken.Wire(advSecret)
	require.NoError(t, err)
	hubKey, err := SchemeHubSHA256.key(advSecret)
	require.NoError(t, err)
	bitKey, err := SchemeBitbucketSHA256.key(advSecret)
	require.NoError(t, err)

	assert.NotEqual(t, shared, string(hubKey),
		"the token replayed in cleartext by every shared-token delivery IS the HMAC key "+
			"of a hub-scheme hook holding the same Secret")
	assert.NotEqual(t, string(hubKey), string(bitKey),
		"one MAC is valid under two schemes, so a signature captured for one is a signature for the other")
}

// TestAdvARefIsNotValidatedTheWayACommitIs. VerifyPush requires the commit to be
// a whole 40- or 64-hex object id and requires the ref to be merely non-empty,
// so anything at all rides out of the verified payload in Push.Ref — including
// the shapes that are dangerous exactly where a ref is spent, on a git command
// line.
func TestAdvARefIsNotValidatedTheWayACommitIs(t *testing.T) {
	k := key(t, "https://"+stubHost+"/acme/pay")
	body := []byte(`{"whatever":true}`)
	commit := strings.Repeat("ab", 20)

	for name, ref := range map[string]string{
		"an option, not a ref":  "--upload-pack=/bin/sh",
		"an embedded newline":   "refs/heads/main\nrefs/heads/evil",
		"an embedded NUL":       "refs/heads/main\x00",
		"a path climb":          "../../../../etc/passwd",
		"not a ref at all":      "just some words",
		"a megabyte of nothing": strings.Repeat("a", 1<<20),
	} {
		t.Run(name, func(t *testing.T) {
			d := &stub{host: stubHost, scheme: SchemeHubSHA256,
				claim: PushClaim{Repository: k, Ref: ref, Commit: commit}}
			reg, err := New(Policy{}, Binding{Host: stubHost, Driver: d})
			require.NoError(t, err)
			hdr, err := Sign(SchemeHubSHA256, advSecret, body, "m", time.Now())
			require.NoError(t, err)

			push, err := reg.VerifyPush(
				Hook{Key: k, Handle: "h", Scheme: SchemeHubSHA256, Secret: advSecret},
				Delivery{Header: hdr, Body: body})
			assert.Error(t, err, "VerifyPush returned Push{Ref:%q}", push.Ref)
		})
	}
}

// TestAdvACraftedCloneURLCannotAimARequestAtAHostNobodyBound is the SSRF
// question. A clone URL is caller-supplied at registration; the key derived
// from it selects the driver, and there is no default driver, so an internal
// address is a refusal and never a request.
func TestAdvACraftedCloneURLCannotAimARequestAtAHostNobodyBound(t *testing.T) {
	reg, err := New(Policy{}, Binding{Host: stubHost, Driver: &stub{host: stubHost, scheme: SchemeHubSHA256}})
	require.NoError(t, err)

	for _, spelling := range []string{
		"https://127.0.0.1/acme/pay",
		"https://127.0.0.1:8080/acme/pay",
		"http://169.254.169.254/latest/meta-data",
		"https://metadata.google.internal/computeMetadata/v1",
		"https://10.0.0.5/acme/pay",
		"https://[::1]/acme/pay",
		"https://localhost:6443/acme/pay",
		"ssh://git@192.168.1.1/acme/pay",
		"https://user:ghp_realtoken@" + stubHost + "/acme/pay",
		"https://" + stubHost + "@169.254.169.254/acme/pay",
		"https://" + stubHost + "%2f..%2f@evil.example/acme/pay",
		"file:///etc/passwd",
		"gopher://127.0.0.1:11211/_stats",
	} {
		t.Run(spelling, func(t *testing.T) {
			k, perr := repokey.Parse(spelling)
			if perr != nil {
				t.Logf("repokey refused it: %v", perr)
				return
			}
			t.Logf("key = %s", k)
			_, err := reg.For(k, Token("a-token"))
			var nd *NoDriverError
			require.ErrorAs(t, err, &nd, "a session was built for host %q", k.Host)

			// And a credential cannot be aimed there through a bound driver's
			// own transport either.
			rt := PinnedTransport(stubHost, nil, func(h http.Header) { h.Set("Authorization", "Bearer secret") })
			req, rerr := http.NewRequest(http.MethodGet, "https://"+k.Host+"/x", nil)
			if rerr == nil {
				_, err := rt.RoundTrip(req)
				var leak *CredentialLeakError
				require.ErrorAs(t, err, &leak)
			}
		})
	}
}

// TestAdvASecretNeverRendersThroughAnEnclosingValue: a Hook holds the secret,
// and a Hook is exactly the sort of value a consumer logs whole.
func TestAdvASecretNeverRendersThroughAnEnclosingValue(t *testing.T) {
	const material = "sup3r-s3cret-hook-material-000000"
	s := SecretFrom([]byte(material))
	h := Hook{Key: key(t, "https://"+stubHost+"/acme/pay"), Handle: "7", Scheme: SchemeHubSHA256, Secret: s}
	cred := Token(material)

	type enclosing struct {
		Hook Hook
		Cred Credential
		Ptr  *Secret
	}
	e := enclosing{Hook: h, Cred: cred, Ptr: &s}

	for _, rendering := range []string{
		fmt.Sprintf("%v", h), fmt.Sprintf("%+v", h), fmt.Sprintf("%#v", h),
		fmt.Sprintf("%v", e), fmt.Sprintf("%+v", e), fmt.Sprintf("%s", e),
		fmt.Sprintf("%v", cred), fmt.Sprintf("%+v", cred), fmt.Sprintf("%x", cred),
		fmt.Sprintf("%v", &h), fmt.Sprintf("%+v", []Hook{h}),
		fmt.Sprintf("%v", map[string]Hook{"a": h}),
	} {
		assert.NotContains(t, rendering, material, "rendering leaked the secret: %s", rendering)
	}
}

// TestAdvAOneByteSecretIsAcceptedEverywhereButOneScheme.
//
// NewSecret mints 32 bytes, but SecretFrom takes any non-empty slice and
// RegisterPushHook checks only Empty(). So a consumer that reads a secret out of
// a config file, or reuses a short string, registers a hook whose HMAC key is
// two base64 characters — brute-forceable offline from a single captured
// delivery, after which every push to that repository can be forged. Only
// SchemeStandardWebhooks refuses it, and it refuses it for a formatting reason
// (GitLab's own whsec_ shape) rather than a strength one.
func TestAdvAOneByteSecretIsAcceptedEverywhereButOneScheme(t *testing.T) {
	weak := SecretFrom([]byte("x"))
	k := key(t, "https://"+stubHost+"/acme/pay")
	d := &stub{host: stubHost, scheme: SchemeHubSHA256, handle: "h",
		claim: PushClaim{Repository: k, Ref: "refs/heads/main", Commit: strings.Repeat("ab", 20)}}
	reg, err := New(Policy{}, Binding{Host: stubHost, Driver: d})
	require.NoError(t, err)

	sess, err := reg.For(k, Token("t"))
	require.NoError(t, err)
	cb, err := neturl.Parse("https://cb.example/hook")
	require.NoError(t, err)

	hook, err := sess.RegisterPushHook(t.Context(), k, cb, weak)
	assert.Error(t, err, "a hook was registered with a %d-byte secret; its wire form is %q",
		len(weak.Expose()), d.lastWire)
	if err != nil {
		return
	}

	body := []byte(`{"anything":true}`)
	hdr, err := Sign(SchemeHubSHA256, weak, body, "m", time.Now())
	require.NoError(t, err)
	_, err = reg.VerifyPush(hook, Delivery{Header: hdr, Body: body})
	assert.Error(t, err, "and a delivery MAC'd with that key verified")
}
