package accounts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	httpsvc "github.com/taubyte/tau/pkg/http"
)

// Minimal HTTP surface for the Member-facing CLI.
//
// Routes (under accounts.tau.<host>):
//
//   POST  /login/start            — body: {email, account_slug?}
//   POST  /login/finish/magic     — body: {code}
//   GET   /me                     — header: Authorization: Bearer tau-session.<...>
//   POST  /logout                 — header: Authorization: Bearer tau-session.<...>
//
// Member-management actions (invite, users, plans) live on the P2P surface.

func (srv *AccountsService) setupHTTPRoutes() {
	// In dev (dream / local), routes register on bare "localhost" so a
	// request to http://localhost:<dream_port>/... matches without a custom
	// Host header. In production the host is `accounts.tau.<network>`,
	// served via the operator's reverse proxy / auto-tls. Matches the
	// services/auth pattern (cf. github_http_endpoints.go).
	host := "localhost"
	if !srv.devMode && srv.rootDomain != "" {
		host = "accounts.tau." + srv.rootDomain
	}

	srv.http.POST(&httpsvc.RouteDefinition{
		Host:    host,
		Path:    "/login/start",
		Handler: srv.httpLoginStart,
	})

	srv.http.POST(&httpsvc.RouteDefinition{
		Host:    host,
		Path:    "/login/finish/magic",
		Handler: srv.httpLoginFinishMagic,
	})

	srv.http.GET(&httpsvc.RouteDefinition{
		Host:    host,
		Path:    "/me",
		Handler: srv.httpMe,
	})

	srv.http.POST(&httpsvc.RouteDefinition{
		Host:    host,
		Path:    "/logout",
		Handler: srv.httpLogout,
	})

	// Member-facing management surface. Body shape mirrors the P2P verbs
	// (action-dispatched), so the wrapper just auth-checks the bearer and
	// forwards. Operator-only entities (accounts, plans) stay P2P-only.
	srv.http.POST(&httpsvc.RouteDefinition{
		Host:    host,
		Path:    "/members",
		Handler: srv.httpManagementHandler(srv.apiMemberHandler),
	})
	srv.http.POST(&httpsvc.RouteDefinition{
		Host:    host,
		Path:    "/users",
		Handler: srv.httpManagementHandler(srv.apiUserHandler),
	})
}

// httpManagementHandler wraps a P2P verb handler as an HTTP route, adding
// Member-session bearer auth and forwarding the body verbatim. P2P verbs
// don't self-authenticate; the threat model is swarm-key on the P2P side
// and Member-session on the HTTP side. Per-role authorisation is a follow-up.
func (srv *AccountsService) httpManagementHandler(
	handler func(context.Context, streams.Connection, command.Body) (cr.Response, error),
) func(httpsvc.Context) (any, error) {
	return func(ctx httpsvc.Context) (any, error) {
		token, err := bearerFromRequest(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := srv.Client().Login().VerifySession(ctx.Request().Context(), token); err != nil {
			return nil, fmt.Errorf("invalid session: %w", err)
		}
		var body command.Body
		if err := ctx.ParseBody(&body); err != nil {
			return nil, fmt.Errorf("parse body: %w", err)
		}
		resp, err := handler(ctx.Request().Context(), nil, body)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// loginStartBody / loginFinishMagicBody are the JSON bodies the CLI sends.
type loginStartBody struct {
	Email       string `json:"email"`
	AccountSlug string `json:"account_slug,omitempty"`
}

type loginFinishMagicBody struct {
	Code string `json:"code"`
}

// meResponse is what GET /me returns. Built by walking the verified Member's
// linked Accounts via the verify endpoint.
type meResponse struct {
	Member   *accountsIface.Member                `json:"member,omitempty"`
	Accounts []accountsIface.VerifyAccountSummary `json:"accounts,omitempty"`
	Session  *accountsIface.Session               `json:"session,omitempty"`
}

func (srv *AccountsService) httpLoginStart(ctx httpsvc.Context) (any, error) {
	var body loginStartBody
	if err := ctx.ParseBody(&body); err != nil {
		return nil, fmt.Errorf("parse body: %w", err)
	}
	if body.Email == "" && body.AccountSlug == "" {
		return nil, errors.New("email or account_slug required")
	}
	cli := srv.Client().Login()
	chal, err := cli.StartManaged(ctx.Request().Context(), accountsIface.StartManagedLoginInput{
		Email:       body.Email,
		AccountSlug: body.AccountSlug,
	})
	if err != nil {
		return nil, err
	}
	return chal, nil
}

func (srv *AccountsService) httpLoginFinishMagic(ctx httpsvc.Context) (any, error) {
	var body loginFinishMagicBody
	if err := ctx.ParseBody(&body); err != nil {
		return nil, fmt.Errorf("parse body: %w", err)
	}
	if body.Code == "" {
		return nil, errors.New("code required")
	}
	sess, err := srv.Client().Login().FinishManagedMagicLink(ctx.Request().Context(), accountsIface.FinishMagicLinkInput{
		Code:     body.Code,
		ClientIP: clientIPFromRequest(ctx),
	})
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// clientIPFromRequest extracts the originating IP for verify-side rate
// limiting. Honours common proxy headers (X-Forwarded-For, X-Real-IP) so
// installs behind a reverse proxy still get per-client (not per-proxy)
// limits. Falls back to RemoteAddr.
func clientIPFromRequest(ctx httpsvc.Context) string {
	r := ctx.Request()
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		// Per RFC, first entry is the originating client.
		if i := strings.Index(v, ","); i > 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return strings.TrimSpace(v)
	}
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i > 0 {
		// IPv6 brackets: "[::1]:1234" → "::1".
		if strings.HasPrefix(addr, "[") {
			if end := strings.LastIndex(addr, "]"); end > 0 {
				return addr[1:end]
			}
		}
		return addr[:i]
	}
	return addr
}

func (srv *AccountsService) httpMe(ctx httpsvc.Context) (any, error) {
	token, err := bearerFromRequest(ctx)
	if err != nil {
		return nil, err
	}
	sess, err := srv.Client().Login().VerifySession(ctx.Request().Context(), token)
	if err != nil {
		return nil, fmt.Errorf("invalid session: %w", err)
	}

	// Load the Member record + the Account it lives in.
	memberStore := newMemberStore(srv.db, sess.AccountID)
	member, err := memberStore.Get(ctx.Request().Context(), sess.MemberID)
	if err != nil {
		return nil, err
	}
	accStore := newAccountStore(srv.db)
	acc, err := accStore.Get(ctx.Request().Context(), sess.AccountID)
	if err != nil {
		return nil, err
	}
	// Build a single-element AccountSummary so the response shape matches the
	// verify-endpoint contract (Members can later be on multiple Accounts via
	// the same email; today the session pins one).
	summary := accountsIface.VerifyAccountSummary{
		ID:   acc.ID,
		Slug: acc.Slug,
		Name: acc.Name,
	}
	return &meResponse{
		Member:   member,
		Accounts: []accountsIface.VerifyAccountSummary{summary},
		Session:  sess,
	}, nil
}

func (srv *AccountsService) httpLogout(ctx httpsvc.Context) (any, error) {
	token, err := bearerFromRequest(ctx)
	if err != nil {
		return nil, err
	}
	if err := srv.Client().Login().Logout(ctx.Request().Context(), token); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

// bearerFromRequest pulls the Member-session bearer from the Authorization
// header. Accepts both `Bearer <token>` and the bare `<token>` forms.
func bearerFromRequest(ctx httpsvc.Context) (string, error) {
	auth := ctx.Request().Header.Get("Authorization")
	if auth == "" {
		return "", errors.New("authorization header required")
	}
	auth = strings.TrimPrefix(auth, "Bearer ")
	if !strings.HasPrefix(auth, sessionBearerPrefix) {
		return "", errors.New("authorization is not a tau session bearer")
	}
	return auth, nil
}
