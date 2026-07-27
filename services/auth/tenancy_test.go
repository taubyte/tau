package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-github/v71/github"
	tauConfig "github.com/taubyte/tau/pkg/config"
	"gotest.tools/v3/assert"
)

func testAppKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.NilError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

// stubGitHub serves the three calls the app flow makes. membership is the body
// returned for the membership lookup; status 0 means 200.
func stubGitHub(t *testing.T, membershipStatus int, membershipBody string, calls *int64) *url.URL {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/orgs/acme/installation", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":424242}`)
	})
	mux.HandleFunc("/app/installations/424242/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"token":"ghs_stub","expires_at":"2099-01-01T00:00:00Z"}`)
	})
	mux.HandleFunc("/orgs/acme/memberships/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(calls, 1)
		if membershipStatus != 0 && membershipStatus != http.StatusOK {
			w.WriteHeader(membershipStatus)
			fmt.Fprint(w, `{"message":"nope"}`)
			return
		}
		fmt.Fprint(w, membershipBody)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL + "/")
	assert.NilError(t, err)
	return u
}

func newTestVerifier(t *testing.T, status int, body string, calls *int64) *githubAppVerifier {
	t.Helper()
	v, err := newGitHubAppVerifier("Iv1.test", testAppKey(t))
	assert.NilError(t, err)
	t.Cleanup(v.Close)
	v.baseURL = stubGitHub(t, status, body, calls)
	return v
}

func TestMembership_ActiveMemberIsAllowedAndCached(t *testing.T) {
	var calls int64
	v := newTestVerifier(t, http.StatusOK, `{"state":"active"}`, &calls)

	for i := 0; i < 3; i++ {
		ok, err := v.IsActiveMember(context.Background(), "acme", "dev")
		assert.NilError(t, err)
		assert.Assert(t, ok, "active member should be allowed")
	}

	// The revocation window is only meaningful if repeats are actually served
	// from cache rather than re-asked.
	assert.Equal(t, atomic.LoadInt64(&calls), int64(1))
}

// A pending invitation is not membership.
func TestMembership_PendingIsRefused(t *testing.T) {
	var calls int64
	v := newTestVerifier(t, http.StatusOK, `{"state":"pending"}`, &calls)

	ok, err := v.IsActiveMember(context.Background(), "acme", "invitee")
	assert.NilError(t, err)
	assert.Assert(t, !ok, "pending invitation must not count as membership")
}

// 404 is the only definitive non-member answer, and it caches.
func TestMembership_NotFoundIsDefinitiveNonMember(t *testing.T) {
	var calls int64
	v := newTestVerifier(t, http.StatusNotFound, "", &calls)

	for i := 0; i < 2; i++ {
		ok, err := v.IsActiveMember(context.Background(), "acme", "stranger")
		assert.NilError(t, err, "a non-member is an answer, not a failure")
		assert.Assert(t, !ok)
	}
	assert.Equal(t, atomic.LoadInt64(&calls), int64(1))
}

// A 403 means the app was blocked or lacks permission. Reporting that as "not a
// member" would send the operator chasing the wrong problem, so it stays an
// error and is never cached as a verdict.
func TestMembership_ForbiddenIsAnErrorNotARefusal(t *testing.T) {
	var calls int64
	v := newTestVerifier(t, http.StatusForbidden, "", &calls)

	ok, err := v.IsActiveMember(context.Background(), "acme", "dev")
	assert.Assert(t, err != nil, "403 must not be reported as a clean refusal")
	assert.Assert(t, !ok)

	_, _ = v.IsActiveMember(context.Background(), "acme", "dev")
	assert.Equal(t, atomic.LoadInt64(&calls), int64(2), "errors must not be cached")
}

func TestMembership_RejectsMalformedKey(t *testing.T) {
	_, err := newGitHubAppVerifier("Iv1.test", "not a pem")
	assert.Assert(t, err != nil)

	_, err = newGitHubAppVerifier("", testAppKey(t))
	assert.Assert(t, err != nil, "an empty client id cannot sign a usable jwt")
}

// --- repository ownership ------------------------------------------------

type repoClientStub struct {
	GitHubClient
	repo *github.Repository
}

func (c repoClientStub) Cur() *github.Repository { return c.repo }

func orgRepo(fullName string) repoClientStub {
	owner, name, _ := strings.Cut(fullName, "/")
	return repoClientStub{repo: &github.Repository{
		Name:     github.Ptr(name),
		FullName: github.Ptr(fullName),
		Owner:    &github.User{Login: github.Ptr(owner)},
	}}
}

func TestAuthorizeRepository(t *testing.T) {
	srv := &AuthService{tenancy: tauConfig.Tenancy{Provider: "github", Owner: "Acme"}}

	assert.NilError(t, srv.authorizeRepository(orgRepo("acme/widget")))
	assert.NilError(t, srv.authorizeRepository(orgRepo("ACME/widget")))

	assert.Assert(t, srv.authorizeRepository(orgRepo("stranger/widget")) != nil)
	assert.Assert(t, srv.authorizeRepository(orgRepo("acmeco/widget")) != nil,
		"a namespace that merely starts with the owner is a different namespace")
	assert.Assert(t, srv.authorizeRepository(repoClientStub{}) != nil)
}

// An unconfigured cloud refuses rather than accepting anything the caller can
// see. This is the behaviour that keeps a community deployment from being open
// to everyone holding a github token.
func TestAuthorizeRepository_UnconfiguredRefuses(t *testing.T) {
	srv := &AuthService{}
	err := srv.authorizeRepository(orgRepo("anyone/widget"))
	assert.ErrorIs(t, err, ErrNoTenancy)
}

func TestAuthorizeRepository_DevModeBypasses(t *testing.T) {
	srv := &AuthService{devMode: true}
	assert.NilError(t, srv.authorizeRepository(orgRepo("anyone/widget")))
}
