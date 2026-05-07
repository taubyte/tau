//go:build dreaming

package tests

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	httpaccounts "github.com/taubyte/tau/clients/http/accounts"
	commonIface "github.com/taubyte/tau/core/common"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/services/accounts"
	"github.com/taubyte/tau/services/accounts/email"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/accounts/dream"
	_ "github.com/taubyte/tau/services/accounts/dream"
)

// TestHTTPEndpoints_E2E_Dreaming validates the full HTTP stack the
// `tau accounts ...` CLI uses: a dream-mode accounts service exposes its
// HTTP port, the clients/http/accounts client hits it, and the auth-side
// round-trips (login start, /me with bad bearer, logout-with-bad-bearer)
// behave correctly.
//
// This proves the auto.New HTTP setup + host-based routing + port allocation
// + the HTTP-client wiring all work end-to-end. Bearer-validated /me and
// logout happy paths are exercised by the in-package handler tests
// (http_endpoints_test.go) using the same code path.
func TestHTTPEndpoints_E2E_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	uname := strings.ReplaceAll(t.Name(), "/", "_")
	u, err := m.New(dream.UniverseConfig{Name: uname})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"accounts": {},
		},
	})
	assert.NilError(t, err)

	// Allow the HTTP listener to bind.
	time.Sleep(500 * time.Millisecond)

	svc := u.Accounts()
	assert.Assert(t, svc != nil, "accounts service did not register")

	// Fish out the dream-allocated HTTP port for the accounts service node.
	port, err := u.GetPortHttp(svc.Node())
	assert.NilError(t, err)
	assert.Assert(t, port != 0, "no http port allocated for accounts service")

	// Seed an Account + Member via the in-process Client so the HTTP login
	// flow has a candidate to authenticate.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cli := svc.Client()
	acc, err := cli.Accounts().Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	assert.NilError(t, err)
	_, err = cli.Members(acc.ID).Invite(ctx, accountsIface.InviteMemberInput{
		PrimaryEmail: "alice@example.com",
		Role:         accountsIface.RoleOwner,
	})
	assert.NilError(t, err)

	// HTTP client against the dream-port server.
	url := fmt.Sprintf("http://localhost:%d", port)
	httpClient, err := httpaccounts.New(ctx,
		httpaccounts.WithURL(url),
		httpaccounts.WithUnsecure())
	assert.NilError(t, err)

	t.Run("login start emits magic-link over HTTP", func(t *testing.T) {
		chal, err := httpClient.LoginStart("alice@example.com", "")
		assert.NilError(t, err, "POST /login/start round-trip")
		assert.Equal(t, chal.MagicLinkSent, true)
	})

	t.Run("login start with unknown email rejects", func(t *testing.T) {
		_, err := httpClient.LoginStart("ghost@example.com", "")
		assert.Assert(t, err != nil, "expected 4xx for unknown email")
	})

	t.Run("login start with empty body rejects", func(t *testing.T) {
		_, err := httpClient.LoginStart("", "")
		assert.Assert(t, err != nil, "expected error for empty body")
	})

	t.Run("login finish-magic with bogus code rejects", func(t *testing.T) {
		_, err := httpClient.FinishMagic("ghost-code")
		assert.Assert(t, err != nil, "expected error for unknown code")
	})

	t.Run("/me without session header rejects", func(t *testing.T) {
		// httpClient was constructed without WithSession, so calling Me
		// returns the client-side guard error (no token configured).
		_, err := httpClient.Me()
		assert.Assert(t, err != nil)
	})

	t.Run("/me with bogus bearer rejects", func(t *testing.T) {
		bogus, err := httpaccounts.New(ctx,
			httpaccounts.WithURL(url),
			httpaccounts.WithUnsecure(),
			httpaccounts.WithSession("tau-session.bogus.bogus"))
		assert.NilError(t, err)

		_, err = bogus.Me()
		assert.Assert(t, err != nil, "expected /me to fail for non-tau-session bearer")
	})

	t.Run("logout without session rejects", func(t *testing.T) {
		err := httpClient.Logout()
		assert.Assert(t, err != nil, "logout without session should fail client-side guard")
	})

	t.Run("full magic-link round-trip + /me + logout + /me-rejects", func(t *testing.T) {
		// Swap in a captured sender so we can fish the magic-link code out
		// of the email body without scraping stdout.
		realSvc, ok := svc.(*accounts.AccountsService)
		if !ok {
			t.Fatalf("accounts service is not the expected type: %T", svc)
		}
		captured := email.NewStdoutSender(io.Discard)
		previous := realSvc.SwapEmailSender(captured)
		t.Cleanup(func() { realSvc.SwapEmailSender(previous) })

		// 1. POST /login/start over HTTP → sender receives the email.
		chal, err := httpClient.LoginStart("alice@example.com", "")
		assert.NilError(t, err)
		assert.Equal(t, chal.MagicLinkSent, true)

		sent := captured.Sent()
		assert.Equal(t, len(sent), 1, "expected exactly one captured email")
		body := sent[0].Body
		// Code prominently appears on its own line in the body now (template change).
		// We also still embed it in the URL; either is parseable but the
		// indented standalone line is the cleanest signal of v1 wire shape.
		idx := strings.Index(body, "code=")
		assert.Assert(t, idx > 0, "body should include the magic-link URL: %s", body)
		rest := body[idx+5:]
		end := strings.IndexAny(rest, "\n \r&")
		if end == -1 {
			end = len(rest)
		}
		code := rest[:end]
		assert.Assert(t, code != "", "extracted magic-link code is empty")

		// 2. POST /login/finish/magic over HTTP → returns a Session.
		sess, err := httpClient.FinishMagic(code)
		assert.NilError(t, err)
		assert.Assert(t, sess.Token != "", "session token empty")

		// 3. GET /me with the issued bearer over HTTP.
		authedClient, err := httpaccounts.New(ctx,
			httpaccounts.WithURL(url),
			httpaccounts.WithUnsecure(),
			httpaccounts.WithSession(sess.Token))
		assert.NilError(t, err)

		me, err := authedClient.Me()
		assert.NilError(t, err)
		assert.Assert(t, me.Member != nil)
		assert.Equal(t, me.Member.PrimaryEmail, "alice@example.com")
		assert.Equal(t, len(me.Accounts), 1)
		assert.Equal(t, me.Accounts[0].Slug, "acme")

		// 4. POST /logout over HTTP → server-side revoke.
		assert.NilError(t, authedClient.Logout())

		// 5. /me with the now-revoked bearer fails.
		_, err = authedClient.Me()
		assert.Assert(t, err != nil, "Me() after Logout should fail")
	})

	t.Run("test-only session helper drives /me directly", func(t *testing.T) {
		// Validates IssueTestSession (the helper compiled in under -tags=dreaming):
		// mint a bearer, then prove it works end-to-end over HTTP without any
		// magic-link interaction. Used by callers that want to skip the
		// email round-trip in larger e2e flows.
		realSvc, ok := svc.(*accounts.AccountsService)
		if !ok {
			t.Fatalf("accounts service is not the expected type: %T", svc)
		}
		bearer, _, _, err := realSvc.IssueTestSessionForMember(ctx, "alice@example.com")
		assert.NilError(t, err)
		assert.Assert(t, bearer != "")

		authed, err := httpaccounts.New(ctx,
			httpaccounts.WithURL(url),
			httpaccounts.WithUnsecure(),
			httpaccounts.WithSession(bearer))
		assert.NilError(t, err)

		me, err := authed.Me()
		assert.NilError(t, err)
		assert.Equal(t, me.Member.PrimaryEmail, "alice@example.com")

		assert.NilError(t, authed.Logout())
	})
}
