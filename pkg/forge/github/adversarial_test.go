package github_test

// THE ROWS WHERE THE FORGE IS THE ADVERSARY. The gitlab driver in this tree has
// a file of these and this package did not, and the two that follow are the
// ones that were missing something rather than merely missing a name.
//
// What they have in common is that NOTHING FAILS. Every case here is a 200, or
// a delivery whose signature verifies, so no status and no MAC says anything is
// wrong — and what arrives is a string somebody else chose, on a path that ends
// in a tau log line. An error string is the most-copied thing in a log.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
)

// TestAdvAForgeCanWriteTheCallersOwnCredentialIntoTausLogs.
//
// A clone URL is the one field of a repository answer this driver MUST read —
// the key is derived from it and from nothing else — and repokey QUOTES WHAT IT
// WAS GIVEN when it refuses one. A clone URL carrying an identity half is
// exactly the shape repokey calls a secret at rest, so a forge that answers 200
// with one has written a string of its choosing into the error, and the forge
// receives the caller's own credential on every request.
//
// Measured before this: the error read
//
//	forge/github: the clone URL of "acme/pay" is not one this layer can name:
//	"https://x-access-token:<token>@host/acme/pay.git" carries a credential ...
//
// through Repository, Access and Create alike. The gitlab driver refuses to
// carry repokey's reason for this reason in so many words; this driver carried
// it and bounded the length instead, which stops a log-length attack and not
// this one.
func TestAdvAForgeCanWriteTheCallersOwnCredentialIntoTausLogs(t *testing.T) {
	const relayed = "ghp_RELAYED_1a2b3c4d5e6f7a8b"

	for name, cloneURL := range map[string]string{
		"none at all": "",
		// It PARSES. Without the measurement it becomes a key, and a key is
		// stored, compared and printed by everything downstream.
		"a name no repository has": "https://127.0.0.1/acme/" + strings.Repeat("x", 5000),
		"an identity half":         "https://x-access-token:" + relayed + "@127.0.0.1/acme/pay.git",
		"not a URL at all":         "::" + relayed,
	} {
		t.Run(name, func(t *testing.T) {
			body := fmt.Sprintf(`{"full_name":%q,"clone_url":%q,"default_branch":"main",
				"permissions":{"admin":true,"push":true,"pull":true}}`,
				strings.Repeat("n", 5000), cloneURL)
			sess, key := sessionOn(t, answer(http.StatusOK, body), "acme/pay")

			for method, err := range map[string]error{
				"Repository": second(sess.Repository(ctx(), key)),
				"Access":     second(sess.Access(ctx(), key)),
				"Create":     second(sess.Create(ctx(), key, forge.CreateOptions{})),
				"List":       sess.ListRepositories(ctx(), func(forge.Repository) bool { return true }),
			} {
				if err == nil {
					continue
				}
				t.Logf("%s: %v", method, err)
				assert.Lessf(t, len(err.Error()), 2000,
					"%s: a forge chose how long a tau log line is (%d bytes)", method, len(err.Error()))
				assert.NotContainsf(t, err.Error(), relayed,
					"%s: a string the forge composed while holding the caller's credential reached an error", method)
				assert.NotContainsf(t, err.Error(), goodToken,
					"%s: the caller's credential reached an error", method)
			}
		})
	}
}

// second drops a value and keeps the error, so the table above reads as one
// line per method.
func second[T any](_ T, err error) error { return err }

// TestAdvADeliveryIsAStrangersCloneURL is the gitlab driver's row of the same
// name. A verified delivery is still a stranger's bytes — the signature proves
// the forge RELAYED them, not that they are true — and the core admits one up
// to eight megabytes. So the clone URL a payload names is measured before it is
// parsed, and the reason it will not parse is not carried.
func TestAdvADeliveryIsAStrangersCloneURL(t *testing.T) {
	const inThePayload = "ghp_INPAYLOAD_9f8e7d6c5b4a"

	for name, cloneURL := range map[string]string{
		"none at all":              "",
		"a name no repository has": "https://github.com/acme/" + strings.Repeat("x", 5000),
		"an identity half":         "https://x-access-token:" + inThePayload + "@github.com/acme/pay.git",
	} {
		t.Run(name, func(t *testing.T) {
			reg, hook := dotComRegistry(t)
			payload := []byte(`{"ref":"refs/heads/main",
				"after":"1f0c9d2b3a4e5f60718293a4b5c6d7e8f9012345",
				"repository":{"full_name":"acme/pay","clone_url":"` + cloneURL + `"}}`)

			_, err := reg.VerifyPush(hook, forge.Delivery{
				Header: pushHeader("sha256=" + signPayload(t, hook.Secret, payload)),
				Body:   payload,
			})
			require.Error(t, err)
			t.Logf("VerifyPush: %v", err)
			assert.Less(t, len(err.Error()), 2000,
				"a delivery chose how long a tau log line is (%d bytes)", len(err.Error()))
			assert.NotContains(t, err.Error(), inThePayload,
				"an identity a delivery carried was written into an error string")
		})
	}
}

// signPayload is what a GitHub sends alongside a body, so the rows above reach
// the driver rather than stopping at the core's verification.
func signPayload(t *testing.T, secret forge.Secret, body []byte) string {
	t.Helper()
	wire, err := forge.SchemeHubSHA256.Wire(secret)
	require.NoError(t, err)
	mac := hmac.New(sha256.New, []byte(wire))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
