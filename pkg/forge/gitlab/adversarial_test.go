package gitlab

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

const advToken = "glpat-ADVERSARY-3f9c1d77b0e4"

func advSess(t *testing.T, h http.Handler, full string) (*forge.Session, repokey.Key) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	drv, err := New(host, BaseURL(srv.URL))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)
	k, err := repokey.Parse("https://" + host + "/" + full)
	require.NoError(t, err)
	sess, err := reg.For(k, forge.Token(advToken))
	require.NoError(t, err)
	return sess, k
}

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

// advWebhookSecret is A FIXED VECTOR AND NOT A MINT, so every near miss in the
// table below is a forgery by construction rather than by luck: a mutation of a
// random signature sometimes is the signature. The honest delivery is asserted
// over a freshly minted secret as well.
const advWebhookSecret = "gitlab-adversarial-hook-secret-#1-padded-to-32b"

func otherThan(b byte) byte {
	if b == 'x' {
		return 'y'
	}
	return 'x'
}

func TestAdvNoForgedDeliveryReachesAPush(t *testing.T) {
	drv, err := New("gitlab.example.invalid", Signing())
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{},
		forge.Binding{Host: "gitlab.example.invalid", Driver: drv})
	require.NoError(t, err)

	k, err := repokey.Parse("https://gitlab.example.invalid/acme/team/pay")
	require.NoError(t, err)
	secret := forge.SecretFrom([]byte(advWebhookSecret))
	wire, err := forge.SchemeStandardWebhooks.Wire(secret)
	require.NoError(t, err)

	body := []byte(`{"object_kind":"push","ref":"refs/heads/main",
		"checkout_sha":"1f0c9d2b3a4e5f60718293a4b5c6d7e8f9012345",
		"project":{"git_http_url":"https://gitlab.example.invalid/acme/team/pay.git"}}`)
	hook := forge.Hook{Key: k, Handle: "1", Scheme: forge.SchemeStandardWebhooks, Secret: secret}

	signed := func(s forge.Secret) http.Header {
		h, err := forge.Sign(forge.SchemeStandardWebhooks, s, body, "msg_1", time.Now())
		require.NoError(t, err)
		h.Set("X-Gitlab-Event", "Push Hook")
		return h
	}
	honest := signed(secret)
	sig := honest.Get("webhook-signature")

	_, err = reg.VerifyPush(hook, forge.Delivery{Header: honest, Body: body})
	require.NoError(t, err, "the honest delivery must still pass")

	minted, err := forge.NewSecret()
	require.NoError(t, err)
	mintedHook := hook
	mintedHook.Secret = minted
	_, err = reg.VerifyPush(mintedHook, forge.Delivery{Header: signed(minted), Body: body})
	require.NoError(t, err, "the honest delivery must pass for a freshly minted secret too")

	for name, mutate := range map[string]func(http.Header){
		"absent":            func(h http.Header) { h.Del("webhook-signature") },
		"empty":             func(h http.Header) { h.Set("webhook-signature", "") },
		"a prefix of it":    func(h http.Header) { h.Set("webhook-signature", sig[:len(sig)-1]) },
		"one character off": func(h http.Header) { h.Set("webhook-signature", sig[:3]+string(otherThan(sig[3]))+sig[4:]) },
		"the wire itself":   func(h http.Header) { h.Set("webhook-signature", "v1,"+wire) },
		// What an instance that does not sign sends: the token, replayed.
		"a replayed token instead": func(h http.Header) {
			h.Del("webhook-signature")
			h.Set("X-Gitlab-Token", wire)
		},
		"another message id": func(h http.Header) { h.Set("webhook-id", "msg_2") },
	} {
		t.Run(name, func(t *testing.T) {
			h := honest.Clone()
			mutate(h)
			_, err := reg.VerifyPush(hook, forge.Delivery{Header: h, Body: body})
			require.Error(t, err, "a forged delivery was accepted")
			assert.NotContains(t, err.Error(), wire, "the error quoted the wire secret")
			assert.NotContains(t, err.Error(), string(secret.Expose()))
		})
	}

	t.Run("a hook recorded under the token scheme GitLab replays", func(t *testing.T) {
		token := hook
		token.Scheme = "shared-token"
		h := honest.Clone()
		h.Set("X-Gitlab-Token", wire)
		_, err := reg.VerifyPush(token, forge.Delivery{Header: h, Body: body})
		require.Error(t, err, "a token-replay scheme is not one tau verifies")
	})
}

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
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: host, Driver: drv})
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

func TestAdvNoInstanceChosenSentenceReachesAnError(t *testing.T) {
	repository := func(s *forge.Session, k repokey.Key) error {
		_, err := s.Repository(context.Background(), k)
		return err
	}
	type testCase struct {
		answer string
		call   func(*forge.Session, repokey.Key) error
	}
	for name, tc := range map[string]testCase{
		"a graphql errors array": {
			answer: `{"data":{"project":null},"errors":[{"message":"could not authenticate $TOKEN"}]}`,
			call: func(s *forge.Session, k repokey.Key) error {
				_, err := s.Access(context.Background(), k)
				return err
			},
		},
		"a project described with no clone url": {
			answer: `{"path_with_namespace":"$TOKEN"}`,
			call:   repository,
		},
		"a clone url carrying a credential": {
			answer: `{"path_with_namespace":"acme/pay","http_url_to_repo":"https://oauth2:$TOKEN@gitlab.example/acme/pay.git"}`,
			call:   repository,
		},
		"a namespace that resolved somewhere else": {
			answer: `{"id":7,"full_path":"$TOKEN"}`,
			call: func(s *forge.Session, k repokey.Key) error {
				_, err := s.Create(context.Background(), k, forge.CreateOptions{})
				return err
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			body := tc.answer
			sess, k := advSess(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, strings.ReplaceAll(body, "$TOKEN", r.Header.Get("PRIVATE-TOKEN")))
			}), "acme/pay")

			err := tc.call(sess, k)
			require.Error(t, err)
			t.Logf("%v", err)
			assert.NotContains(t, err.Error(), advToken,
				"%s wrote the caller's credential into an error string", name)
		})
	}
}

func TestAdvAnInstanceCannotChooseHowLongATauLogLineIs(t *testing.T) {
	const wide = 200000
	for name, body := range map[string]string{
		"a project path": `{"path_with_namespace":"` + strings.Repeat("x", wide) + `"}`,
		"a clone url":    `{"path_with_namespace":"acme/pay","http_url_to_repo":"https://` + strings.Repeat("x", wide) + `"}`,
		"a clone url host": `{"path_with_namespace":"acme/pay","http_url_to_repo":"https://` +
			strings.Repeat("x", wide) + `.example/acme/pay.git"}`,
	} {
		t.Run(name, func(t *testing.T) {
			sess, k := advSess(t, answer(t, http.StatusOK, body), "acme/pay")
			_, err := sess.Repository(context.Background(), k)
			require.Error(t, err)
			assert.Less(t, len(err.Error()), 2000,
				"%s: an instance chose how long a tau log line is (%d bytes)", name, len(err.Error()))
		})
	}
}

func TestAdvADeliveryIsAStrangersCloneURL(t *testing.T) {
	const inThePayload = "glpat-RELAYED-8c31f0d5b7a2"

	drv, err := New("gitlab.example.invalid", Signing())
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{},
		forge.Binding{Host: "gitlab.example.invalid", Driver: drv})
	require.NoError(t, err)

	k, err := repokey.Parse("https://gitlab.example.invalid/acme/pay")
	require.NoError(t, err)
	secret, err := forge.NewSecret()
	require.NoError(t, err)
	hook := forge.Hook{Key: k, Handle: "1", Scheme: forge.SchemeStandardWebhooks, Secret: secret}

	for name, cloneURL := range map[string]string{
		"none at all": "",
		// It PARSES. Without the measurement it becomes a key, and a key is
		// stored, compared and printed by everything downstream.
		"a name no repository has": "https://gitlab.example.invalid/acme/" + strings.Repeat("x", 5000),
		"an identity half":         "https://oauth2:" + inThePayload + "@gitlab.example.invalid/acme/pay.git",
	} {
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"object_kind":"push","ref":"refs/heads/main",
				"checkout_sha":"1f0c9d2b3a4e5f60718293a4b5c6d7e8f9012345",
				"project":{"git_http_url":"` + cloneURL + `"}}`)
			header, err := forge.Sign(forge.SchemeStandardWebhooks, secret, body, "msg_1", time.Now())
			require.NoError(t, err)
			header.Set("X-Gitlab-Event", "Push Hook")
			_, err = reg.VerifyPush(hook, forge.Delivery{Header: header, Body: body})
			require.Error(t, err)
			assert.Less(t, len(err.Error()), 2000,
				"a delivery chose how long a tau log line is (%d bytes)", len(err.Error()))
			assert.NotContains(t, err.Error(), inThePayload,
				"an identity a delivery carried was written into an error string")
		})
	}
}
