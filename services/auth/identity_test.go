//go:build !ee

package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-github/v71/github"
	"github.com/taubyte/tau/core/p2p/keypair"
	tauConfig "github.com/taubyte/tau/pkg/config"
	httpAuth "github.com/taubyte/tau/pkg/http/auth"
	"gotest.tools/v3/assert"
)

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

func TestGitHubTokenHTTPAuth_NoToken_Rejects(t *testing.T) {
	srv, cleanup := CreateTestService(t, nil)
	defer cleanup()

	ctx := authCtx(t, "", "")
	if _, err := srv.GitHubTokenHTTPAuth(ctx); err == nil {
		t.Fatalf("expected rejection when no token")
	}
}

// meClientStub reports a login and nothing else.
type meClientStub struct {
	GitHubClient
	login string
}

func (c meClientStub) Me() *github.User {
	if c.login == "" {
		return nil
	}
	return &github.User{Login: github.Ptr(c.login)}
}

type stubVerifier struct {
	member bool
	err    error
}

func (s stubVerifier) IsActiveMember(context.Context, string, string) (bool, error) {
	return s.member, s.err
}

func ossService(v MembershipVerifier) *AuthService {
	return &AuthService{
		tenancy:    tauConfig.Tenancy{Provider: "github", Owner: "acme"},
		membership: v,
	}
}

func TestAuthorizeIdentity_MemberAllowedNonMemberRefused(t *testing.T) {
	srv := ossService(stubVerifier{member: true})
	assert.NilError(t, srv.authorizeIdentity(context.Background(), nil, meClientStub{login: "dev"}))

	srv = ossService(stubVerifier{member: false})
	err := srv.authorizeIdentity(context.Background(), nil, meClientStub{login: "stranger"})
	assert.Assert(t, err != nil)
	assert.Assert(t, strings.Contains(err.Error(), "acme"), "the refusal names the namespace: %v", err)
}

// A provider failure is relayed, not reported as a refusal — the two mean very
// different things to whoever has to fix it.
func TestAuthorizeIdentity_ProviderErrorIsRelayed(t *testing.T) {
	sentinel := context.DeadlineExceeded
	srv := ossService(stubVerifier{err: sentinel})

	err := srv.authorizeIdentity(context.Background(), nil, meClientStub{login: "dev"})
	assert.ErrorIs(t, err, sentinel)
}

func TestAuthorizeIdentity_UnknownLoginRefused(t *testing.T) {
	srv := ossService(stubVerifier{member: true})
	assert.Assert(t, srv.authorizeIdentity(context.Background(), nil, meClientStub{}) != nil)
}

// This build has no accounts service to fall back on, so a cloud with no
// namespace has no way to answer "may this identity use the API" — it must not
// start outside dev mode.
func TestInitIdentity_ProdRequiresTenancy(t *testing.T) {
	srv := &AuthService{}
	cfg, err := tauConfig.New(tauConfig.WithDevMode(false), tauConfig.WithNetworkFqdn("example.com"),
		tauConfig.WithPrivateKey(keypair.NewRaw()))
	assert.NilError(t, err)

	assert.ErrorIs(t, srv.initIdentity(cfg), ErrNoTenancy)
}

func TestInitIdentity_DevModeDoesNotRequireTenancy(t *testing.T) {
	srv := &AuthService{devMode: true}
	cfg, err := tauConfig.New(tauConfig.WithDevMode(true), tauConfig.WithNetworkFqdn("example.com"))
	assert.NilError(t, err)

	assert.NilError(t, srv.initIdentity(cfg))
	assert.Assert(t, srv.membership == nil, "no namespace means nothing to verify against")
}

func TestInitIdentity_ProdTenancyBuildsVerifier(t *testing.T) {
	srv := &AuthService{}
	cfg, err := tauConfig.New(
		tauConfig.WithDevMode(false),
		tauConfig.WithNetworkFqdn("example.com"),
		tauConfig.WithPrivateKey(keypair.NewRaw()),
		tauConfig.WithTenancy(tauConfig.Tenancy{
			Provider: "github",
			Owner:    "acme",
			App:      tauConfig.TenancyApp{ClientId: "Iv1.test", Key: testAppKey(t)},
		}),
	)
	assert.NilError(t, err)

	assert.NilError(t, srv.initIdentity(cfg))
	assert.Assert(t, srv.membership != nil)
}
