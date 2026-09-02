package gitlab

// ADVERSARIAL. Everything here is httptest or in-process.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/v2/forge"
)

const advToken = "glpat-ADVERSARY-3f9c1d77b0e4"

func advSess(t *testing.T, h http.Handler, full string) (*forge.Session, repokey.Key) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	drv, err := New(host, BaseURL(srv.URL))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{AllowUnboundSchemes: true}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)
	k, err := repokey.Parse("https://" + host + "/" + full)
	require.NoError(t, err)
	sess, err := reg.For(k, forge.Token(advToken))
	require.NoError(t, err)
	return sess, k
}

// TestAdvAnInstanceCanWriteTheCallersOwnCredentialIntoTausLogs.
//
// fault renders the instance's own words — message(r.body) — into the error for
// every status that is not 401 or a 404 naming a repository. The instance
// receives the caller's PRIVATE-TOKEN on every request, so it can put that token
// in the sentence, and the sentence becomes an error string, and an error string
// is the most-copied thing in a log.
//
// The github driver in this same tree refuses to read a non-2xx body at all, for
// exactly this reason, in so many words: "a host that can choose what lands in
// your logs can choose to put your own credential there". The two drivers
// disagree about a threat the layer names.
func TestAdvAnInstanceCanWriteTheCallersOwnCredentialIntoTausLogs(t *testing.T) {
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"message":"could not authenticate %s, try again"}`,
			r.Header.Get("PRIVATE-TOKEN"))
	})
	sess, k := advSess(t, echo, "acme/pay")
	ctx := context.Background()

	seen := map[string]error{}
	_, err := sess.Whoami(ctx)
	seen["Whoami"] = err
	_, err = sess.Access(ctx, k)
	seen["Access"] = err
	_, err = sess.Repository(ctx, k)
	seen["Repository"] = err
	seen["Delete"] = sess.Delete(ctx, k)
	_, err = sess.InstallDeployKey(ctx, k, "t", "ssh-ed25519 AAAA")
	seen["InstallDeployKey"] = err
	seen["ListRepositories"] = sess.ListRepositories(ctx, func(forge.Repository) bool { return true })

	for name, err := range seen {
		require.Error(t, err, name)
		t.Logf("%-18s -> %v", name, err)
		assert.NotContains(t, err.Error(), advToken, "%s wrote the caller's credential into an error string", name)
	}
}

// TestAdvAccessNeverReadsAFailureAsAVerdict. Every way the instance can fail to
// answer, and in none of them may a level come back with a nil error.
func TestAdvAccessNeverReadsAFailureAsAVerdict(t *testing.T) {
	for name, h := range map[string]http.Handler{
		"500": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }),
		"403": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }),
		"429": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }),
		"401": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }),
		"a captive portal answering 200 in html": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "<html>sign in to the wifi</html>")
		}),
		"200 with a graphql errors array": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"data":{"project":null},"errors":[{"message":"boom"}]}`)
		}),
		"200 with nothing in it": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{}`)
		}),
		"a redirect off the host": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://evil.example/graphql", http.StatusFound)
		}),
		"a connection dropped mid-answer": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Skip("no hijacker")
			}
			conn, _, err := hj.Hijack()
			require.NoError(t, err)
			conn.Close()
		}),
	} {
		t.Run(name, func(t *testing.T) {
			sess, k := advSess(t, h, "acme/pay")
			got, err := sess.Access(context.Background(), k)
			t.Logf("Access = %v, err = %v", got, err)
			if err == nil {
				assert.Equal(t, forge.AccessNone, got,
					"a level was returned with no error for %q", name)
				assert.False(t, got.CanWrite())
				return
			}
			assert.Equal(t, forge.AccessNone, got, "an error must carry the zero level")
		})
	}
}

// TestAdvNoForgedDeliveryReachesAPush drives the whole VerifyPush path with a
// real gitlab driver and every wrong token an attacker can present.
func TestAdvNoForgedDeliveryReachesAPush(t *testing.T) {
	drv, err := New("gitlab.example.invalid")
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{AllowUnboundSchemes: true},
		forge.Binding{Host: "gitlab.example.invalid", Driver: drv})
	require.NoError(t, err)

	k, err := repokey.Parse("https://gitlab.example.invalid/acme/team/pay")
	require.NoError(t, err)
	secret, err := forge.NewSecret()
	require.NoError(t, err)
	wire, err := forge.SchemeSharedToken.Wire(secret)
	require.NoError(t, err)

	body := []byte(`{"object_kind":"push","ref":"refs/heads/main",
		"checkout_sha":"1f0c9d2b3a4e5f60718293a4b5c6d7e8f9012345",
		"project":{"git_http_url":"https://gitlab.example.invalid/acme/team/pay.git"}}`)
	hook := forge.Hook{Key: k, Handle: "1", Scheme: forge.SchemeSharedToken, Secret: secret}

	deliver := func(token string) (forge.Push, error) {
		h := http.Header{}
		h.Set("X-Gitlab-Event", "Push Hook")
		if token != "\x00absent" {
			h.Set("X-Gitlab-Token", token)
		}
		return reg.VerifyPush(hook, forge.Delivery{Header: h, Body: body})
	}

	_, err = deliver(wire)
	require.NoError(t, err, "the honest delivery must still pass")

	for name, token := range map[string]string{
		"absent":            "\x00absent",
		"empty":             "",
		"a prefix of it":    wire[:len(wire)-1],
		"one character off": "x" + wire[1:],
		"case folded":       strings.ToLower(wire),
		"the raw secret":    string(secret.Expose()),
		"a whsec_ spelling": "whsec_" + wire,
		"a plausible guess": "glpat-guess",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := deliver(token)
			require.Error(t, err, "a delivery presenting %q was accepted", token)
			assert.NotContains(t, err.Error(), wire, "the error quoted the wire secret")
			assert.NotContains(t, err.Error(), string(secret.Expose()))
		})
	}

	t.Run("a hook whose scheme was recorded as the signing one", func(t *testing.T) {
		signing := hook
		signing.Scheme = forge.SchemeStandardWebhooks
		h := http.Header{}
		h.Set("X-Gitlab-Event", "Push Hook")
		h.Set("X-Gitlab-Token", wire)
		_, err := reg.VerifyPush(signing, forge.Delivery{Header: h, Body: body})
		require.Error(t, err, "the scheme stored with the hook is what a delivery is checked under")
	})
}

// TestAdvAKeySegmentCannotWalkOutOfItsPathSegment: repokey's grammar accepts
// characters an API path gives meaning to, so the driver's escaping is what
// stops one from becoming a different request.
func TestAdvAKeySegmentCannotWalkOutOfItsPathSegment(t *testing.T) {
	var got []string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		w.WriteHeader(http.StatusNotFound)
	})
	// %3F is "?" and %23 is "#": url.Parse decodes both into the path, so
	// repokey keeps them inside one segment.
	k, err := repokey.Parse("https://h.example/acme/pa%3Fadmin=1")
	require.NoError(t, err)
	t.Logf("key = %s (name %q)", k, k.Name)

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	drv, err := New(host, BaseURL(srv.URL))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{AllowUnboundSchemes: true}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)
	crafted := repokey.Key{Host: host, Owner: k.Owner, Name: k.Name}
	sess, err := reg.For(crafted, forge.Token(advToken))
	require.NoError(t, err)

	sess.Repository(context.Background(), crafted)
	require.NotEmpty(t, got)
	t.Logf("the driver sent %q", got[0])
	assert.NotContains(t, got[0], "?admin=1", "a name walked out of its path segment into the query")
	assert.Contains(t, got[0], "%3F")
}
