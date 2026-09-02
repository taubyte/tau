package frontend

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVerifySignatureAcceptsExactlyOneThing(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)

	require.NoError(t, verifySignature(testSecret, body, sign(testSecret, body)))
}

func TestVerifySignatureRefusesEverythingElse(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	good := sign(testSecret, body)

	sha1mac := hmac.New(sha1.New, []byte(testSecret))
	sha1mac.Write(body)

	for _, tc := range []struct {
		name   string
		secret string
		body   []byte
		header string
		want   error
	}{
		{"no secret at all", "", body, good, errNoSecret},
		{"no header", testSecret, body, "", errBadSignature},
		{"no prefix", testSecret, body, strings.TrimPrefix(good, signaturePrefix), errBadSignature},
		{"wrong prefix", testSecret, body, "sha1=" + hex.EncodeToString(sha1mac.Sum(nil)), errBadSignature},
		{"not hex", testSecret, body, signaturePrefix + strings.Repeat("g", 64), errBadSignature},
		{"too short", testSecret, body, signaturePrefix + "0011", errBadSignature},
		{"too long", testSecret, body, signaturePrefix + strings.Repeat("00", 64), errBadSignature},
		{"another secret", testSecret, body, sign("other", body), errBadSignature},
		{"another body", testSecret, []byte(`{"ref":"refs/heads/other"}`), good, errBadSignature},
		{"one bit flipped", testSecret, body, flipLast(good), errBadSignature},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorIs(t, verifySignature(tc.secret, tc.body, tc.header), tc.want)
		})
	}
}

// The header GitHub still sends beside the sha256 one, and the header v1
// verifies. It is never read: signatureHeader is the only name webhook.go
// looks up, and nothing anywhere reads legacySignatureHeader but this test.
func TestTheSHA1HeaderIsNeverRead(t *testing.T) {
	require.NotEqual(t, signatureHeader, legacySignatureHeader)
	require.Equal(t, "X-Hub-Signature", legacySignatureHeader)
}

func flipLast(signature string) string {
	last := signature[len(signature)-1]
	if last == '0' {
		return signature[:len(signature)-1] + "1"
	}
	return signature[:len(signature)-1] + "0"
}
