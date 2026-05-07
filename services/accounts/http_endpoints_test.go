package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	httpsvc "github.com/taubyte/tau/pkg/http"
)

// mockHTTPCtx is a minimal http.Context implementation for testing the route
// handlers in isolation. The real services use the auto.New http service;
// here we just exercise the handlers' own logic.
type mockHTTPCtx struct {
	body      []byte
	headers   http.Header
	variables map[string]any
}

func newMockCtx() *mockHTTPCtx {
	return &mockHTTPCtx{
		headers:   make(http.Header),
		variables: make(map[string]any),
	}
}

func (m *mockHTTPCtx) HandleWith(_ httpsvc.Handler) error    { return nil }
func (m *mockHTTPCtx) HandleAuth(_ httpsvc.Handler) error    { return nil }
func (m *mockHTTPCtx) HandleCleanup(_ httpsvc.Handler) error { return nil }
func (m *mockHTTPCtx) Body() []byte                          { return m.body }
func (m *mockHTTPCtx) SetBody(b []byte)                      { m.body = b }
func (m *mockHTTPCtx) Variables() map[string]interface{}     { return m.variables }
func (m *mockHTTPCtx) SetVariable(k string, v interface{})   { m.variables[k] = v }
func (m *mockHTTPCtx) GetVariable(k string) (interface{}, error) {
	if v, ok := m.variables[k]; ok {
		return v, nil
	}
	return nil, errors.New("not found")
}
func (m *mockHTTPCtx) GetStringVariable(k string) (string, error) {
	v, err := m.GetVariable(k)
	if err != nil {
		return "", err
	}
	s, ok := v.(string)
	if !ok {
		return "", errors.New("not a string")
	}
	return s, nil
}
func (m *mockHTTPCtx) GetStringArrayVariable(string) ([]string, error)             { return nil, nil }
func (m *mockHTTPCtx) GetStringMapVariable(string) (map[string]interface{}, error) { return nil, nil }
func (m *mockHTTPCtx) GetIntVariable(string) (int, error)                          { return 0, nil }
func (m *mockHTTPCtx) RawResponse() bool                                           { return false }
func (m *mockHTTPCtx) SetRawResponse(bool)                                         {}
func (m *mockHTTPCtx) ParseBody(into any) error                                    { return json.Unmarshal(m.body, into) }
func (m *mockHTTPCtx) Request() *http.Request {
	req, _ := http.NewRequestWithContext(context.Background(), "GET", "/", nil)
	req.Header = m.headers
	return req
}
func (m *mockHTTPCtx) Writer() http.ResponseWriter { return nil }

// --- HTTP handler tests --------------------------------------------

func TestHTTPLoginStart_BadBody(t *testing.T) {
	srv, _ := loginTestService(t)
	ctx := newMockCtx()
	ctx.body = []byte("not json")
	if _, err := srv.httpLoginStart(ctx); err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestHTTPLoginStart_RequiredFields(t *testing.T) {
	srv, _ := loginTestService(t)
	ctx := newMockCtx()
	ctx.body = []byte(`{}`)
	if _, err := srv.httpLoginStart(ctx); err == nil {
		t.Fatalf("expected error for empty body")
	}
}

func TestHTTPLoginStart_MagicLinkSent(t *testing.T) {
	srv, _ := loginTestService(t)
	cliInProc := newInProcessClient(srv)
	ctxBg := context.Background()
	acc, _ := cliInProc.Accounts().Create(ctxBg, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	_, _ = cliInProc.Members(acc.ID).Invite(ctxBg, accountsIface.InviteMemberInput{
		PrimaryEmail: "alice@example.com", Role: accountsIface.RoleOwner,
	})

	ctx := newMockCtx()
	ctx.body = []byte(`{"email":"alice@example.com"}`)
	resp, err := srv.httpLoginStart(ctx)
	if err != nil {
		t.Fatalf("httpLoginStart: %v", err)
	}
	chal, ok := resp.(*accountsIface.ManagedLoginChallenge)
	if !ok {
		t.Fatalf("response wrong type: %T", resp)
	}
	if !chal.MagicLinkSent {
		t.Fatalf("expected MagicLinkSent=true")
	}
}

func TestHTTPLoginFinishMagic_BadCode(t *testing.T) {
	srv, _ := loginTestService(t)
	ctx := newMockCtx()
	ctx.body = []byte(`{"code":""}`)
	if _, err := srv.httpLoginFinishMagic(ctx); err == nil {
		t.Fatalf("expected error for empty code")
	}
	ctx.body = []byte(`{"code":"ghost"}`)
	if _, err := srv.httpLoginFinishMagic(ctx); err == nil {
		t.Fatalf("expected error for unknown code")
	}
}

func TestHTTPMe_RequiresAuth(t *testing.T) {
	srv, _ := loginTestService(t)
	ctx := newMockCtx()
	if _, err := srv.httpMe(ctx); err == nil {
		t.Fatalf("expected error without Authorization header")
	}
}

func TestHTTPMe_HappyPath(t *testing.T) {
	srv, _ := loginTestService(t)
	cliInProc := newInProcessClient(srv)
	ctxBg := context.Background()
	acc, _ := cliInProc.Accounts().Create(ctxBg, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	mem, _ := cliInProc.Members(acc.ID).Invite(ctxBg, accountsIface.InviteMemberInput{
		PrimaryEmail: "alice@example.com", Role: accountsIface.RoleOwner,
	})

	_, bearer, err := srv.sessions.Issue(ctxBg, acc.ID, mem.ID)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	ctx := newMockCtx()
	ctx.headers.Set("Authorization", "Bearer "+bearer)
	resp, err := srv.httpMe(ctx)
	if err != nil {
		t.Fatalf("httpMe: %v", err)
	}
	me, ok := resp.(*meResponse)
	if !ok {
		t.Fatalf("wrong type: %T", resp)
	}
	if me.Member == nil || me.Member.ID != mem.ID {
		t.Fatalf("member wrong: %+v", me.Member)
	}
	if len(me.Accounts) != 1 || me.Accounts[0].Slug != "acme" {
		t.Fatalf("accounts wrong: %+v", me.Accounts)
	}
}

func TestHTTPMe_BadBearer(t *testing.T) {
	srv, _ := loginTestService(t)
	ctx := newMockCtx()
	ctx.headers.Set("Authorization", "Bearer not-a-tau-session")
	if _, err := srv.httpMe(ctx); err == nil {
		t.Fatalf("expected error for bogus bearer")
	}
}

func TestHTTPLogout_RoundTrip(t *testing.T) {
	srv, _ := loginTestService(t)
	ctxBg := context.Background()
	_, bearer, err := srv.sessions.Issue(ctxBg, "acct-1", "mem-1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	ctx := newMockCtx()
	ctx.headers.Set("Authorization", "Bearer "+bearer)
	resp, err := srv.httpLogout(ctx)
	if err != nil {
		t.Fatalf("httpLogout: %v", err)
	}
	if okMap, ok := resp.(map[string]bool); !ok || !okMap["ok"] {
		t.Fatalf("logout response wrong: %+v", resp)
	}

	if _, _, err := srv.sessions.Verify(ctxBg, bearer); err == nil {
		t.Fatalf("session should be revoked")
	}
}

func TestHTTPLogout_RequiresAuth(t *testing.T) {
	srv, _ := loginTestService(t)
	ctx := newMockCtx()
	if _, err := srv.httpLogout(ctx); err == nil {
		t.Fatalf("expected error without Authorization")
	}
}

func TestBearerFromRequest_VariousFormats(t *testing.T) {
	srv, _ := loginTestService(t)
	ctxBg := context.Background()
	_, bearer, _ := srv.sessions.Issue(ctxBg, "a", "m")

	// With "Bearer " prefix.
	ctx := newMockCtx()
	ctx.headers.Set("Authorization", "Bearer "+bearer)
	got, err := bearerFromRequest(ctx)
	if err != nil || got != bearer {
		t.Fatalf("Bearer-prefixed: %v %q", err, got)
	}

	// Without "Bearer " prefix (still accepts).
	ctx = newMockCtx()
	ctx.headers.Set("Authorization", bearer)
	got, err = bearerFromRequest(ctx)
	if err != nil || got != bearer {
		t.Fatalf("bare bearer: %v %q", err, got)
	}

	// Empty header → error.
	ctx = newMockCtx()
	if _, err := bearerFromRequest(ctx); err == nil {
		t.Fatalf("expected empty-header error")
	}

	// Wrong format → error.
	ctx = newMockCtx()
	ctx.headers.Set("Authorization", "Bearer github_pat_xxx")
	if _, err := bearerFromRequest(ctx); err == nil {
		t.Fatalf("expected error for non-tau bearer")
	}
}
