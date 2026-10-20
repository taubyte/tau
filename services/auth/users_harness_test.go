package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/services/auth/password"
)

// The users half's fixture: an in-memory kvdb, a map for a vault, a forge
// driver whose tokens are a map, and an OAuth server standing in for the
// forge's.

// cheap is the Argon2 cost tests hash with.
var cheap = password.Params{Time: 1, Memory: 64, Threads: 1, KeyLen: 32}

type memVault struct {
	mu   sync.Mutex
	m    map[string][]byte
	fail error
	gets int
}

func newVault() *memVault { return &memVault{m: map[string][]byte{}} }

func (v *memVault) Get(_ context.Context, p, e, n string) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.gets++
	if v.fail != nil {
		return nil, v.fail
	}
	b, ok := v.m[p+"/"+e+"/"+n]
	if !ok {
		return nil, errors.New("not set")
	}
	return b, nil
}

func (v *memVault) Set(_ context.Context, p, e, n string, b []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.fail != nil {
		return v.fail
	}
	v.m[p+"/"+e+"/"+n] = b
	return nil
}

func (v *memVault) List(_ context.Context, p, e string) ([]string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.fail != nil {
		return nil, v.fail
	}
	var out []string
	for k := range v.m {
		if n, ok := strings.CutPrefix(k, p+"/"+e+"/"); ok {
			out = append(out, n)
		}
	}
	return out, nil
}

// testDriver is a forge whose tokens are a map from token to identity.
type testDriver struct {
	host   string
	oauth  *forge.OAuth
	mu     sync.Mutex
	tokens map[string]forge.Identity
	broken bool
}

type signInDriver struct{ *testDriver }

func (d signInDriver) OAuth() forge.OAuth { return *d.oauth }

func (d *testDriver) Host() string                  { return d.host }
func (d *testDriver) Addressable(repokey.Key) error { return nil }
func (d *testDriver) Bind(c forge.Credential) (forge.Client, error) {
	return &testClient{d: d, token: string(c.Expose())}, nil
}

func (d *testDriver) give(token string, id forge.Identity) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.tokens[token] = id
}

type testClient struct {
	d     *testDriver
	token string
}

func (c *testClient) Host() string { return c.d.host }
func (c *testClient) Whoami(context.Context) (forge.Identity, error) {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	if c.d.broken {
		return forge.Identity{}, errors.New("forge down")
	}
	id, ok := c.d.tokens[c.token]
	if !ok {
		return forge.Identity{}, &forge.CredentialError{Host: c.d.host, Reason: "bad token"}
	}
	return id, nil
}
func (c *testClient) Access(context.Context, repokey.Key) (forge.Access, error) {
	return forge.AccessNone, nil
}
func (c *testClient) Repository(context.Context, repokey.Key) (forge.Repository, error) {
	return forge.Repository{}, errors.New("unused")
}

// oauthServer is a forge's OAuth endpoints. A test plays the browser: it
// reads the authorize URL, approves it with grant, and gets the code the
// forge would have redirected with.
type oauthServer struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	codes    map[string]grant
	devices  map[string]*device
	secret   string
	lastForm url.Values
	idToken  func(nonce string) string
}

type grant struct {
	challenge, redirect, nonce string
	token                      string
}

type device struct {
	approved string
	denied   bool
}

func newOAuthServer(t *testing.T) *oauthServer {
	o := &oauthServer{t: t, codes: map[string]grant{}, devices: map[string]*device{}, secret: "client-secret"}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", o.handleToken)
	mux.HandleFunc("/device", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		o.mu.Lock()
		code := "dc-" + r.Form.Get("client_id") + "-" + time.Now().Format("150405.000000000")
		o.devices[code] = &device{}
		o.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"device_code": code, "user_code": "ABCD-EFGH", "verification_uri": o.srv.URL + "/activate",
			"verification_uri_complete": o.srv.URL + "/activate?code=ABCD-EFGH", "interval": 1, "expires_in": 600,
		})
	})
	o.srv = httptest.NewServer(mux)
	t.Cleanup(o.srv.Close)
	return o
}

func (o *oauthServer) endpoints(device bool) *forge.OAuth {
	f := &forge.OAuth{AuthorizeURL: o.srv.URL + "/authorize", TokenURL: o.srv.URL + "/token", Scopes: []string{"repo"}, PKCE: true, SecretInBody: true}
	if device {
		f.DeviceURL = o.srv.URL + "/device"
	}
	return f
}

// approve plays the browser on an authorize URL: the forge issues code for
// token, and the redirect carries it with the state.
func (o *oauthServer) approve(authURL, token string) (code, state string) {
	u, err := url.Parse(authURL)
	require.NoError(o.t, err)
	q := u.Query()
	code = "code-" + q.Get("state")[:8]
	o.mu.Lock()
	o.codes[code] = grant{challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), nonce: q.Get("nonce"), token: token}
	o.mu.Unlock()
	return code, q.Get("state")
}

func (o *oauthServer) handleToken(w http.ResponseWriter, r *http.Request) {
	require.NoError(o.t, r.ParseForm())
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastForm = r.Form
	fail := func(code string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": code})
	}
	if r.Form.Get("client_secret") != o.secret {
		fail("invalid_client")
		return
	}
	var token, nonce string
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		g, ok := o.codes[r.Form.Get("code")]
		if !ok || g.redirect != r.Form.Get("redirect_uri") || g.challenge != challenge(r.Form.Get("code_verifier")) {
			fail("invalid_grant")
			return
		}
		delete(o.codes, r.Form.Get("code"))
		token, nonce = g.token, g.nonce
	case "urn:ietf:params:oauth:grant-type:device_code":
		d, ok := o.devices[r.Form.Get("device_code")]
		switch {
		case !ok || d.denied:
			fail("access_denied")
			return
		case d.approved == "":
			fail("authorization_pending")
			return
		}
		token = d.approved
	default:
		fail("unsupported_grant_type")
		return
	}
	body := map[string]any{"access_token": token, "token_type": "bearer", "scope": "repo,read:user", "refresh_token": "forge-refresh", "expires_in": 3600}
	if o.idToken != nil {
		body["id_token"] = o.idToken(nonce)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}

func (o *oauthServer) approveDevice(code, token string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.devices[code].approved = token
}

const testHost = "forge.example"

type fx struct {
	t      *testing.T
	s      *Service
	db     kvdb.KVDB
	vault  *memVault
	forge  *testDriver
	oauth  *oauthServer
	reg    *forge.Registry
	now    time.Time
	adminP string
}

func newFx(t *testing.T, mutate ...func(*Config)) *fx {
	t.Helper()
	f := &fx{t: t, vault: newVault(), now: time.Unix(1_800_000_000, 0), adminP: "admin password"}
	f.oauth = newOAuthServer(t)
	f.forge = &testDriver{host: testHost, oauth: f.oauth.endpoints(true), tokens: map[string]forge.Identity{}}
	plain := &testDriver{host: "plain.example", tokens: map[string]forge.Identity{}}
	reg, err := forge.New(forge.Policy{},
		forge.Binding{Host: testHost, Driver: signInDriver{f.forge}},
		forge.Binding{Host: plain.host, Driver: plain})
	require.NoError(t, err)
	f.reg = reg
	db, err := kvdbmock.New().New(nil, "auth", 0)
	require.NoError(t, err)
	f.db = db

	h, err := password.New(f.adminP, "", nil, cheap)
	require.NoError(t, err)
	phc, err := h.Encode()
	require.NoError(t, err)
	cfg := Config{
		Issuer:    "tau:test",
		Admin:     Admin{Name: "root", PasswordHash: phc},
		Forges:    []ForgeApp{{Host: testHost, ClientID: "client-1"}},
		Redirects: []string{"https://console.example/auth/callback"},
		Params:    cheap,
	}
	for _, m := range mutate {
		m(&cfg)
	}
	f.vault.m[vaultProject+"/"+vaultEnvironment+"/"+OAuthSecretName(testHost)] = []byte(f.oauth.secret)
	f.s = newTestService(t, WithUsers(db, f.vault, cfg), WithForges(reg), WithHTTPClient(f.oauth.srv.Client()), WithClock(func() time.Time { return f.now }))
	return f
}

func (f *fx) sessions() *sessionHandler { return &sessionHandler{f.s} }
func (f *fx) users() *userHandler       { return &userHandler{f.s} }
func (f *fx) identity() *identityHandler {
	return &identityHandler{f.s}
}

func req[T any](msg *T, bearer string) *connect.Request[T] {
	r := connect.NewRequest(msg)
	if bearer != "" {
		r.Header().Set("Authorization", "Bearer "+bearer)
	}
	return r
}

func code(t *testing.T, err error) connect.Code {
	t.Helper()
	require.Error(t, err)
	var ce *connect.Error
	require.True(t, errors.As(err, &ce), "%v is not a connect error", err)
	return ce.Code()
}

// adminToken signs the built-in admin in.
func (f *fx) adminToken() string {
	f.t.Helper()
	resp, err := f.sessions().Login(f.t.Context(), req(&authv2.LoginRequest{Name: "root", Password: f.adminP}, ""))
	require.NoError(f.t, err)
	return resp.Msg.GetSession().GetAccessToken()
}

// local makes a local user and signs them in.
func (f *fx) local(name string, role authv2.Role, caps ...authv2.Capability) (*authv2.User, string) {
	f.t.Helper()
	pw := ""
	for _, c := range caps {
		if c == authv2.Capability_CAPABILITY_LOGIN {
			pw = name + " password"
		}
	}
	u, err := f.users().CreateUser(f.t.Context(), req(&authv2.CreateUserRequest{Name: name, Role: role, Capabilities: caps, Password: pw}, f.adminToken()))
	require.NoError(f.t, err)
	if pw == "" {
		return u.Msg, ""
	}
	resp, err := f.sessions().Login(f.t.Context(), req(&authv2.LoginRequest{Name: name, Password: pw}, ""))
	require.NoError(f.t, err)
	return u.Msg, resp.Msg.GetSession().GetAccessToken()
}

func identity(subject, login string) forge.Identity {
	return forge.Identity{Subject: subject, Login: login, Email: login + "@Example.com"}
}
