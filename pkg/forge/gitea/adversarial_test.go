package gitea_test

// Rows where the forge is the adversary and nothing fails: a 200, or a verified
// delivery, carrying a string somebody else chose toward a tau log line.

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

func second[T any](_ T, err error) error { return err }

func TestAdvADeliveryIsAStrangersCloneURL(t *testing.T) {
	const inThePayload = "ghp_INPAYLOAD_9f8e7d6c5b4a"

	for name, cloneURL := range map[string]string{
		"none at all":              "",
		"a name no repository has": "https://gitea.example/acme/" + strings.Repeat("x", 5000),
		"an identity half":         "https://x-access-token:" + inThePayload + "@gitea.example/acme/pay.git",
	} {
		t.Run(name, func(t *testing.T) {
			reg, hook := instanceRegistry(t)
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

// signPayload is what a Gitea sends alongside a body, so the rows above reach
// the driver rather than stopping at the core's verification.
func signPayload(t *testing.T, secret forge.Secret, body []byte) string {
	t.Helper()
	wire, err := forge.SchemeHubSHA256.Wire(secret)
	require.NoError(t, err)
	mac := hmac.New(sha256.New, []byte(wire))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
