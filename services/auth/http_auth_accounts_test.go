package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	httpAuth "github.com/taubyte/tau/pkg/http/auth"
)

// fakeAccountsClient is a minimal implementation of
// core/services/accounts.Client for verify-call tests. Only Verify is wired;
// other methods return errNotImpl to make accidental use loud.
type fakeAccountsClient struct {
	verifyFn func(ctx context.Context, provider, externalID string) (*accountsIface.VerifyResponse, error)
}

var errNotImpl = errors.New("fakeAccountsClient: method not implemented in this test")

func (f *fakeAccountsClient) Verify(ctx context.Context, provider, externalID string) (*accountsIface.VerifyResponse, error) {
	if f.verifyFn != nil {
		return f.verifyFn(ctx, provider, externalID)
	}
	return nil, errNotImpl
}
func (f *fakeAccountsClient) ResolvePlan(context.Context, string, string, string, string) (*accountsIface.ResolveResponse, error) {
	return nil, errNotImpl
}
func (f *fakeAccountsClient) Accounts() accountsIface.Accounts          { return nil }
func (f *fakeAccountsClient) Members(string) accountsIface.Members      { return nil }
func (f *fakeAccountsClient) Users(string) accountsIface.Users          { return nil }
func (f *fakeAccountsClient) Plans(string) accountsIface.Plans          { return nil }
func (f *fakeAccountsClient) Login() accountsIface.Login                { return nil }
func (f *fakeAccountsClient) Peers(...peerCore.ID) accountsIface.Client { return f }
func (f *fakeAccountsClient) Close()                                    {}

// authCtx builds a context carrying a value-typed httpAuth.Authorization so
// GetAuthorization (which type-asserts on the value type) returns it.
func authCtx(t *testing.T, tokenType, token string) *mockHTTPContextWithComplexVars {
	t.Helper()
	c := &mockHTTPContextWithComplexVars{variables: map[string]interface{}{}}
	if tokenType != "" {
		c.variables["Authorization"] = httpAuth.Authorization{Type: tokenType, Token: token}
	}
	return c
}

func TestGitHubTokenHTTPAuth_NoAccountsClient_PassThrough(t *testing.T) {
	srv, cleanup := CreateTestService(t, nil)
	defer cleanup()

	// No accounts client → universal-rule check is skipped (legacy behavior).
	srv.accountsClient = nil
	srv.newGitHubClient = func(ctx context.Context, token string) (GitHubClient, error) {
		return &mockGitHubClient{}, nil
	}

	ctx := authCtx(t, "github", "tok")
	if _, err := srv.GitHubTokenHTTPAuth(ctx); err != nil {
		t.Fatalf("expected no-error pass-through, got %v", err)
	}
}

func TestGitHubTokenHTTPAuth_AccountsLinked_Allows(t *testing.T) {
	srv, cleanup := CreateTestService(t, nil)
	defer cleanup()

	srv.accountsURL = "https://accounts.test.tau"
	srv.accountsClient = &fakeAccountsClient{
		verifyFn: func(_ context.Context, provider, externalID string) (*accountsIface.VerifyResponse, error) {
			if provider != "github" {
				t.Errorf("provider = %q, want github", provider)
			}
			if externalID != "12345" {
				t.Errorf("externalID = %q, want 12345 (from mockGitHubClient.Me())", externalID)
			}
			return &accountsIface.VerifyResponse{
				Linked: true,
				Accounts: []accountsIface.VerifyAccountSummary{
					{Slug: "acme", Plans: []accountsIface.VerifyPlanSummary{{Slug: "prod", IsDefault: true}}},
				},
			}, nil
		},
	}
	srv.newGitHubClient = func(ctx context.Context, token string) (GitHubClient, error) {
		return &mockGitHubClient{}, nil // .Me() returns ID=12345
	}

	ctx := authCtx(t, "github", "tok")
	if _, err := srv.GitHubTokenHTTPAuth(ctx); err != nil {
		t.Fatalf("expected linked → no error, got %v", err)
	}

	// LinkedAccounts ctx var should be set.
	v, err := ctx.GetVariable("LinkedAccounts")
	if err != nil {
		t.Fatalf("LinkedAccounts not set on context: %v", err)
	}
	accs, ok := v.([]accountsIface.VerifyAccountSummary)
	if !ok {
		t.Fatalf("LinkedAccounts has wrong type: %T", v)
	}
	if len(accs) != 1 || accs[0].Slug != "acme" {
		t.Fatalf("LinkedAccounts wrong content: %+v", accs)
	}
}

func TestGitHubTokenHTTPAuth_AccountsNotLinked_RejectsWithSignupURL(t *testing.T) {
	srv, cleanup := CreateTestService(t, nil)
	defer cleanup()

	srv.accountsURL = "https://accounts.test.tau"
	srv.accountsClient = &fakeAccountsClient{
		verifyFn: func(_ context.Context, _, _ string) (*accountsIface.VerifyResponse, error) {
			return &accountsIface.VerifyResponse{Linked: false}, nil
		},
	}
	srv.newGitHubClient = func(ctx context.Context, token string) (GitHubClient, error) {
		return &mockGitHubClient{}, nil
	}

	ctx := authCtx(t, "github", "tok")
	_, err := srv.GitHubTokenHTTPAuth(ctx)
	if err == nil {
		t.Fatalf("expected rejection for not-linked")
	}
	if !strings.Contains(err.Error(), "no tau account linked") {
		t.Fatalf("expected 'no tau account linked' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "https://accounts.test.tau") {
		t.Fatalf("expected accounts URL in error, got: %v", err)
	}
}

func TestGitHubTokenHTTPAuth_VerifyError_RejectsWithErrorContext(t *testing.T) {
	srv, cleanup := CreateTestService(t, nil)
	defer cleanup()

	srv.accountsURL = "https://accounts.test.tau"
	srv.accountsClient = &fakeAccountsClient{
		verifyFn: func(_ context.Context, _, _ string) (*accountsIface.VerifyResponse, error) {
			return nil, errors.New("accounts service unreachable")
		},
	}
	srv.newGitHubClient = func(ctx context.Context, token string) (GitHubClient, error) {
		return &mockGitHubClient{}, nil
	}

	ctx := authCtx(t, "github", "tok")
	_, err := srv.GitHubTokenHTTPAuth(ctx)
	if err == nil {
		t.Fatalf("expected rejection on verify error")
	}
	if !strings.Contains(err.Error(), "accounts verify failed") {
		t.Fatalf("expected verify-failed wrap, got: %v", err)
	}
}

func TestGitHubTokenHTTPAuth_NoToken_Rejects(t *testing.T) {
	srv, cleanup := CreateTestService(t, nil)
	defer cleanup()

	// No Authorization on context.
	ctx := authCtx(t, "", "")
	if _, err := srv.GitHubTokenHTTPAuth(ctx); err == nil {
		t.Fatalf("expected rejection when no token")
	}
}
