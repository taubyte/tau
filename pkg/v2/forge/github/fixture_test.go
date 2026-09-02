package github_test

// The GitHub this package's tests talk to is an httptest server and nothing
// else. NOTHING HERE REACHES github.com: a test that needs the network is a
// test that is green when the network is, and a signature check proven against
// a live forge is a signature check nobody can re-run.
//
// What the fixture serves is the eleven endpoints the driver calls, in GitHub's
// own shapes, with GitHub's own status codes for the cases that matter — 401 on
// a revoked token, 404 on a repository this credential cannot see, 403 on an
// organization it may not create in, 422 on a name already taken. The app
// assertion is VERIFIED here with the public half of the test key, so "the
// driver signs an RS256 JWT correctly" is asserted rather than assumed.

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/v2/forge"
	"github.com/taubyte/tau/pkg/v2/forge/forgetest"
)

const (
	// Long enough and odd enough that finding either in a URL or an error
	// string is unambiguous, which is what the suite's credential canary reads.
	goodToken = "gh-tok-9c4f1ae2d7b6"
	badToken  = "gh-tok-revoked-77b1e3"

	login  = "octo-tau"
	userID = 4711

	appClientID    = "Iv23liEXAMPLECLIENT"
	installationID = 4242

	namespace = "acme"
	member    = "alice"
	pending   = "pat"
	nonMember = "mallory"

	apiPrefix = "/api/v3/"
)

// testKey is the app's RSA key, generated once per test binary. It is generated
// rather than checked in so that no private key of any kind lives in this tree,
// even a worthless one.
var testKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
})

func testKeyPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(testKey()),
	})
}

type perm struct{ admin, maintain, push, pull bool }

var (
	permAdmin = perm{admin: true, maintain: true, push: true, pull: true}
	permPush  = perm{push: true, pull: true}
	permPull  = perm{pull: true}
)

type fRepo struct {
	owner, name   string
	private       bool
	defaultBranch string
	perm          perm
}

type fHook struct {
	full     string
	callback string
	wire     string
}

type fKey struct {
	full     string
	title    string
	pub      string
	readOnly bool
}

// fixture is one in-memory GitHub behind a TLS httptest server.
type fixture struct {
	srv  *httptest.Server
	rt   *breakableRT
	host string

	// renames are the repositories this GitHub answers for under a name they no
	// longer have. It is written once, before the server is handed out, and
	// read without the lock for that reason.
	renames map[string]string

	mu            sync.Mutex
	repos         map[string]*fRepo
	hooks         map[string]*fHook
	keys          map[string]*fKey
	installTokens map[string]bool
	seq           int64
	requests      []forgetest.Request
	lastWire      string
	lastRO        bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		repos:         make(map[string]*fRepo),
		hooks:         make(map[string]*fHook),
		keys:          make(map[string]*fKey),
		installTokens: make(map[string]bool),
		seq:           1000,
	}
	f.srv = httptest.NewTLSServer(f.handler())
	t.Cleanup(f.srv.Close)
	f.host = strings.TrimPrefix(f.srv.URL, "https://")
	f.rt = &breakableRT{base: f.srv.Client().Transport}

	// The fixtures the conformance suite names, seeded to the grants it asserts.
	f.seed("acme", "pay", true, permPush)      // Writable
	f.seed("acme", "public", false, permPull)  // ReadOnly: readable, no grant
	f.seed("acme", "site", true, permAdmin)    // Admin
	f.seed("acme", "scratch", true, permAdmin) // Deletable
	// NoAccess (stranger/secret) and Missing (acme/nope) are deliberately not
	// seeded: GitHub hides a repository a credential may not see behind the same
	// 404 it gives one that does not exist, and pretending otherwise here would
	// test a forge nobody runs.

	// acme/billing was renamed to acme/pay, and this GitHub still answers for
	// the old name — with the new repository, whose full_name and clone URL say
	// so. It is the fixture the derivation contract is asserted on: everywhere
	// else the key asked about and the key answered with are the same string,
	// so a driver echoing its own argument back is indistinguishable from one
	// reading the answer.
	//
	// IT RESOLVES THE OLD NAME RATHER THAN REDIRECTING TO IT, and it must:
	// GitHub answers a rename with 301 and this driver follows no redirect at
	// all (see noRedirect, and the reason it is right), so a 3xx here would
	// assert that policy a second time instead of asserting derivation. What
	// the contract says is that the KEY COMES FROM THE ANSWER, and the answer
	// is what a body says about which repository it describes.
	f.renames = map[string]string{"acme/billing": "acme/pay"}
	return f
}

func (f *fixture) seed(owner, name string, private bool, p perm) {
	f.repos[owner+"/"+name] = &fRepo{
		owner: owner, name: name, private: private, defaultBranch: "main", perm: p,
	}
}

func (f *fixture) Host() string                 { return f.host }
func (f *fixture) Transport() http.RoundTripper { return f.rt }

func (f *fixture) key(t *testing.T, full string) repokey.Key {
	t.Helper()
	k, err := repokey.Parse("https://" + f.host + "/" + full + ".git")
	if err != nil {
		t.Fatalf("fixture: %q is not a key: %v", full, err)
	}
	return k
}

// Requests reports what the server saw since the last call, and resets. It is
// how the suite proves a refusal happened BEFORE the wire, and it is what the
// credential canary reads.
func (f *fixture) Requests() []forgetest.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.requests
	f.requests = nil
	return out
}

// Break fails the next request AT THE TRANSPORT rather than answering it with a
// 500: an outage is not a status code, and the invariant being asserted is that
// a driver which never reached the forge cannot produce a verdict.
func (f *fixture) Break()   { f.rt.broken.Store(true) }
func (f *fixture) Unbreak() { f.rt.broken.Store(false) }

func (f *fixture) LastDeployKeyReadOnly() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastRO
}

func (f *fixture) DeployKeyInstalled(h forge.Handle) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.keys[string(h)]
	return ok
}

func (f *fixture) LastHookWire() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastWire
}

func (f *fixture) HookInstalled(h forge.Handle) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.hooks[string(h)]
	return ok
}

// breakableRT is the outage. It sits under the credential pin, so a broken call
// never reaches the server and is never recorded — which is exactly what an
// unreachable host looks like.
type breakableRT struct {
	base   http.RoundTripper
	broken atomic.Bool
}

func (b *breakableRT) RoundTrip(r *http.Request) (*http.Response, error) {
	if b.broken.Load() {
		return nil, fmt.Errorf("fixture: the forge is unreachable")
	}
	return b.base.RoundTrip(r)
}

// ─────────────────────────────── the server ───────────────────────────────

func (f *fixture) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET "+apiPrefix+"user", f.getUser)
	mux.HandleFunc("GET "+apiPrefix+"user/repos", f.listRepos)
	mux.HandleFunc("POST "+apiPrefix+"user/repos", f.createUserRepo)
	mux.HandleFunc("POST "+apiPrefix+"orgs/{org}/repos", f.createOrgRepo)
	mux.HandleFunc("GET "+apiPrefix+"repos/{owner}/{name}", f.getRepo)
	mux.HandleFunc("DELETE "+apiPrefix+"repos/{owner}/{name}", f.deleteRepo)
	mux.HandleFunc("POST "+apiPrefix+"repos/{owner}/{name}/keys", f.createKey)
	mux.HandleFunc("DELETE "+apiPrefix+"repos/{owner}/{name}/keys/{id}", f.deleteKey)
	mux.HandleFunc("POST "+apiPrefix+"repos/{owner}/{name}/hooks", f.createHook)
	mux.HandleFunc("DELETE "+apiPrefix+"repos/{owner}/{name}/hooks/{id}", f.deleteHook)
	mux.HandleFunc("GET "+apiPrefix+"orgs/{org}/installation", f.getInstallation)
	mux.HandleFunc("POST "+apiPrefix+"app/installations/{id}/access_tokens", f.mintInstallationToken)
	mux.HandleFunc("GET "+apiPrefix+"orgs/{org}/memberships/{user}", f.getMembership)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, forgetest.Request{
			Method: r.Method,
			URL:    "https://" + r.Host + r.URL.RequestURI(),
			Header: r.Header.Clone(),
		})
		f.mu.Unlock()
		mux.ServeHTTP(w, r)
	})
}

// fail answers the way GitHub does: a status and a JSON message. The message is
// never anything the caller sent, and in particular never a token — a fixture
// that echoed one would make the credential canary pass for the wrong reason.
func (f *fixture) fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"message": message})
}

func (f *fixture) ok(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// user gates every endpoint a CALLER'S token reaches.
func (f *fixture) user(w http.ResponseWriter, r *http.Request) bool {
	if bearer(r) == goodToken {
		return true
	}
	f.fail(w, http.StatusUnauthorized, "Bad credentials")
	return false
}

// appJWT gates the two endpoints the DEPLOYMENT's credential reaches, and it
// really verifies the assertion: RS256, this app's issuer, unexpired.
func (f *fixture) appJWT(w http.ResponseWriter, r *http.Request) bool {
	tok, err := jwt.ParseWithClaims(bearer(r), &jwt.RegisteredClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("fixture: app assertion is signed with %v, not RS256", t.Method.Alg())
		}
		return &testKey().PublicKey, nil
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(appClientID))
	if err != nil || !tok.Valid {
		f.fail(w, http.StatusUnauthorized, "A JSON web token could not be decoded")
		return false
	}
	return true
}

// installationToken gates the membership question, which no caller's token and
// no app assertion may answer — only a token minted for the installation.
func (f *fixture) installationToken(w http.ResponseWriter, r *http.Request) bool {
	f.mu.Lock()
	ok := f.installTokens[bearer(r)]
	f.mu.Unlock()
	if ok {
		return true
	}
	f.fail(w, http.StatusUnauthorized, "Bad credentials")
	return false
}

func (f *fixture) getUser(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	f.ok(w, http.StatusOK, map[string]any{
		"login": login,
		"id":    userID,
		"email": login + "@example.invalid",
	})
}

func (f *fixture) repoBody(r *fRepo) map[string]any {
	full := r.owner + "/" + r.name
	return map[string]any{
		"full_name":      full,
		"clone_url":      "https://" + f.host + "/" + full + ".git",
		"ssh_url":        "ssh://git@" + f.host + "/" + full + ".git",
		"default_branch": r.defaultBranch,
		"private":        r.private,
		"permissions": map[string]bool{
			"admin": r.perm.admin, "maintain": r.perm.maintain,
			"push": r.perm.push, "pull": r.perm.pull,
		},
	}
}

func (f *fixture) getRepo(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	full := r.PathValue("owner") + "/" + r.PathValue("name")
	if to, moved := f.renames[full]; moved {
		full = to
	}
	f.mu.Lock()
	repo, ok := f.repos[full]
	var body map[string]any
	if ok {
		body = f.repoBody(repo)
	}
	f.mu.Unlock()
	if !ok {
		f.fail(w, http.StatusNotFound, "Not Found")
		return
	}
	f.ok(w, http.StatusOK, body)
}

func (f *fixture) listRepos(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	f.mu.Lock()
	out := make([]map[string]any, 0, len(f.repos))
	for _, full := range sortedKeys(f.repos) {
		out = append(out, f.repoBody(f.repos[full]))
	}
	f.mu.Unlock()
	if page > 1 {
		out = nil // one page of fixtures; paging itself is asserted separately
	}
	f.ok(w, http.StatusOK, out)
}

func (f *fixture) createUserRepo(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	f.create(w, r, login)
}

func (f *fixture) createOrgRepo(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	org := r.PathValue("org")
	if org != namespace {
		// GitHub's answer for an organization this credential may not create in.
		f.fail(w, http.StatusForbidden, "Resource not accessible by personal access token")
		return
	}
	f.create(w, r, org)
}

func (f *fixture) create(w http.ResponseWriter, r *http.Request, owner string) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Private     bool   `json:"private"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		f.fail(w, http.StatusUnprocessableEntity, "Invalid request")
		return
	}
	f.mu.Lock()
	full := owner + "/" + body.Name
	if _, taken := f.repos[full]; taken {
		f.mu.Unlock()
		f.fail(w, http.StatusUnprocessableEntity, "Repository creation failed: name already exists on this account")
		return
	}
	f.seed(owner, body.Name, body.Private, permAdmin)
	created := f.repoBody(f.repos[full])
	f.mu.Unlock()
	f.ok(w, http.StatusCreated, created)
}

func (f *fixture) deleteRepo(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	full := r.PathValue("owner") + "/" + r.PathValue("name")
	f.mu.Lock()
	repo, ok := f.repos[full]
	if ok && repo.perm.admin {
		delete(f.repos, full)
	}
	f.mu.Unlock()
	switch {
	case !ok:
		f.fail(w, http.StatusNotFound, "Not Found")
	case !repo.perm.admin:
		f.fail(w, http.StatusForbidden, "Must have admin rights to Repository")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// adminRepo is the guard every sub-resource shares: the repository must exist
// and this credential must be able to administer it.
func (f *fixture) adminRepo(w http.ResponseWriter, r *http.Request) (string, bool) {
	full := r.PathValue("owner") + "/" + r.PathValue("name")
	f.mu.Lock()
	repo, ok := f.repos[full]
	f.mu.Unlock()
	switch {
	case !ok:
		f.fail(w, http.StatusNotFound, "Not Found")
		return "", false
	case !repo.perm.admin:
		f.fail(w, http.StatusForbidden, "Must have admin rights to Repository")
		return "", false
	}
	return full, true
}

func (f *fixture) nextID() int64 {
	f.seq++
	return f.seq
}

func (f *fixture) createKey(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	full, ok := f.adminRepo(w, r)
	if !ok {
		return
	}
	var body struct {
		Title    string `json:"title"`
		Key      string `json:"key"`
		ReadOnly bool   `json:"read_only"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
		f.fail(w, http.StatusUnprocessableEntity, "Invalid request")
		return
	}
	f.mu.Lock()
	id := f.nextID()
	f.keys[strconv.FormatInt(id, 10)] = &fKey{full: full, title: body.Title, pub: body.Key, readOnly: body.ReadOnly}
	f.lastRO = body.ReadOnly
	f.mu.Unlock()
	f.ok(w, http.StatusCreated, map[string]any{"id": id})
}

func (f *fixture) deleteKey(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	if _, ok := f.adminRepo(w, r); !ok {
		return
	}
	id := r.PathValue("id")
	f.mu.Lock()
	_, ok := f.keys[id]
	if ok {
		delete(f.keys, id)
	}
	f.mu.Unlock()
	if !ok {
		f.fail(w, http.StatusNotFound, "Not Found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (f *fixture) createHook(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	full, ok := f.adminRepo(w, r)
	if !ok {
		return
	}
	var body struct {
		Name   string   `json:"name"`
		Active bool     `json:"active"`
		Events []string `json:"events"`
		Config struct {
			URL         string `json:"url"`
			ContentType string `json:"content_type"`
			InsecureSSL string `json:"insecure_ssl"`
			Secret      string `json:"secret"`
		} `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Config.URL == "" {
		f.fail(w, http.StatusUnprocessableEntity, "Invalid request")
		return
	}
	f.mu.Lock()
	id := f.nextID()
	f.hooks[strconv.FormatInt(id, 10)] = &fHook{full: full, callback: body.Config.URL, wire: body.Config.Secret}
	f.lastWire = body.Config.Secret
	f.mu.Unlock()
	f.ok(w, http.StatusCreated, map[string]any{"id": id})
}

func (f *fixture) deleteHook(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	if _, ok := f.adminRepo(w, r); !ok {
		return
	}
	id := r.PathValue("id")
	f.mu.Lock()
	_, ok := f.hooks[id]
	if ok {
		delete(f.hooks, id)
	}
	f.mu.Unlock()
	if !ok {
		f.fail(w, http.StatusNotFound, "Not Found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (f *fixture) getInstallation(w http.ResponseWriter, r *http.Request) {
	if !f.appJWT(w, r) {
		return
	}
	if r.PathValue("org") != namespace {
		f.fail(w, http.StatusNotFound, "Not Found")
		return
	}
	f.ok(w, http.StatusOK, map[string]any{"id": installationID})
}

func (f *fixture) mintInstallationToken(w http.ResponseWriter, r *http.Request) {
	if !f.appJWT(w, r) {
		return
	}
	if r.PathValue("id") != strconv.Itoa(installationID) {
		f.fail(w, http.StatusNotFound, "Not Found")
		return
	}
	var body struct {
		Permissions map[string]string `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.fail(w, http.StatusUnprocessableEntity, "Invalid request")
		return
	}
	// The narrowest token the question needs. A driver that asked for everything
	// the app holds would be handing a wider credential to a membership check.
	if body.Permissions["members"] != "read" {
		f.fail(w, http.StatusUnprocessableEntity, "The permissions requested are not granted to this installation")
		return
	}
	f.mu.Lock()
	tok := "ghs_installation_" + strconv.FormatInt(f.nextID(), 10)
	f.installTokens[tok] = true
	f.mu.Unlock()
	f.ok(w, http.StatusCreated, map[string]any{
		"token":      tok,
		"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
}

func (f *fixture) getMembership(w http.ResponseWriter, r *http.Request) {
	if !f.installationToken(w, r) {
		return
	}
	if r.PathValue("org") != namespace {
		f.fail(w, http.StatusNotFound, "Not Found")
		return
	}
	switch r.PathValue("user") {
	case member:
		f.ok(w, http.StatusOK, map[string]any{"state": "active", "role": "member"})
	case pending:
		// An invitation nobody accepted, which is not membership.
		f.ok(w, http.StatusOK, map[string]any{"state": "pending", "role": "member"})
	default:
		f.fail(w, http.StatusNotFound, "Not Found")
	}
}

func sortedKeys(m map[string]*fRepo) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// A listing with a stable order makes a failure name one repository rather
	// than a different one on every run.
	slices.Sort(out)
	return out
}
