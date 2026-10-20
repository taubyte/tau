package bitbucket_test

// The Bitbucket Cloud this package's tests talk to is an httptest server and
// nothing else. NOTHING HERE REACHES bitbucket.org, and there is no container
// suite for this driver because Bitbucket Cloud is not software anybody can
// run: what stands in for it is this server replaying the API's own documented
// shapes (developer.atlassian.com/cloud/bitbucket/rest) — paged {values, next}
// envelopes, the clone links with the caller's username in them, a hook's
// handle as a braced UUID, 400 for a slug already taken, 403 for a workspace
// the caller may not create in, 404 for a repository it may not see.
//
// ONE DEVIATION, and it is the fixture's and not the driver's: Bitbucket spells
// a repository's SSH link scp-style, git@bitbucket.org:acme/pay.git. A loopback
// test host carries a port, and host:port:path is not a spelling anything can
// read back, so the SSH link here is the ssh:// form of the same address.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/forgetest"
	"github.com/taubyte/tau/pkg/git/repokey"
)

const (
	// Long enough and odd enough that finding either in a URL or an error
	// string is unambiguous, which is what the suite's credential canary reads.
	goodToken = "bb-tok-9c4f1ae2d7b6"
	badToken  = "bb-tok-revoked-77b1e3"

	login    = "octo-tau"
	userUUID = "{2f6e5a3c-0b1d-4e7f-9a8b-1c2d3e4f5a6b}"

	namespace = "acme"

	apiPrefix = "/2.0/"

	// fixturePageLen is the page size this server clamps every listing to, far
	// below what the driver asks for — Bitbucket clamps pagelen too — so every
	// listing in the suite really is paged.
	fixturePageLen = 2
)

type fRepo struct {
	owner, name   string
	private       bool
	defaultBranch string
	perm          string // "admin", "write", "read", or "" for no grant
}

type fHook struct {
	full     string
	callback string
	wire     string
}

type fKey struct {
	full  string
	label string
	pub   string
}

// fixture is one in-memory Bitbucket Cloud behind a TLS httptest server.
type fixture struct {
	srv  *httptest.Server
	rt   *breakableRT
	host string

	// renames are the repositories this server answers for under a name they no
	// longer have. Written once, before the server is handed out.
	renames map[string]string

	mu             sync.Mutex
	repos          map[string]*fRepo
	hooks          map[string]*fHook
	keys           map[string]*fKey
	seq            int64
	lastWire       string
	redirect       string
	redirectStatus int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		repos: make(map[string]*fRepo),
		hooks: make(map[string]*fHook),
		keys:  make(map[string]*fKey),
		seq:   1000,
	}
	f.srv = httptest.NewTLSServer(f.handler())
	t.Cleanup(f.srv.Close)
	f.host = strings.TrimPrefix(f.srv.URL, "https://")
	f.rt = &breakableRT{base: f.srv.Client().Transport}

	f.seed("acme", "pay", true, "write")     // Writable
	f.seed("acme", "public", false, "")      // ReadOnly: public, no grant
	f.seed("acme", "site", true, "admin")    // Admin
	f.seed("acme", "scratch", true, "admin") // Deletable
	// NoAccess (stranger/secret) and Missing (acme/nope) are deliberately not
	// seeded: Bitbucket answers a private repository a caller may not see with
	// the same 404 it gives one that does not exist.

	// acme/billing was renamed to acme/pay and this server still answers for the
	// old name with the new repository, so a driver echoing its argument back is
	// distinguishable from one reading the answer.
	f.renames = map[string]string{"acme/billing": "acme/pay"}
	return f
}

func (f *fixture) seed(owner, name string, private bool, perm string) {
	f.repos[owner+"/"+name] = &fRepo{owner: owner, name: name, private: private, defaultBranch: "main", perm: perm}
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

func (f *fixture) Requests() []forgetest.Request { return f.rt.drain() }

func (f *fixture) Break()   { f.rt.broken.Store(true) }
func (f *fixture) Unbreak() { f.rt.broken.Store(false) }

func (f *fixture) RedirectOnce(loc string, status int) {
	f.mu.Lock()
	f.redirect, f.redirectStatus = loc, status
	f.mu.Unlock()
}

// LastDeployKeyReadOnly is true because Bitbucket Cloud has no other kind of
// deploy key: the API has no field that could make one writable, and createKey
// below refuses a body carrying any field but the two the API defines, so a
// driver that tried to ask for one fails here rather than passing.
func (f *fixture) LastDeployKeyReadOnly() bool { return true }

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

// breakableRT is the outage, and the recorder. It records BEFORE it decides,
// under forge.PinnedTransport, so what it records is the hop after the
// credential was attached.
type breakableRT struct {
	base   http.RoundTripper
	broken atomic.Bool

	mu   sync.Mutex
	sent []forgetest.Request
}

func (b *breakableRT) drain() []forgetest.Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.sent
	b.sent = nil
	return out
}

func (b *breakableRT) RoundTrip(r *http.Request) (*http.Response, error) {
	b.mu.Lock()
	b.sent = append(b.sent, forgetest.Request{Method: r.Method, URL: r.URL.String(), Header: r.Header.Clone()})
	b.mu.Unlock()
	if b.broken.Load() {
		return nil, fmt.Errorf("fixture: the forge is unreachable")
	}
	return b.base.RoundTrip(r)
}

// ─────────────────────────────── the server ───────────────────────────────

func (f *fixture) handler() http.Handler {
	mux := http.NewServeMux()
	repo := apiPrefix + "repositories/{owner}/{name}"

	mux.HandleFunc("GET "+apiPrefix+"user", f.getUser)
	mux.HandleFunc("GET "+apiPrefix+"repositories", f.listRepos)
	mux.HandleFunc("GET "+apiPrefix+"user/permissions/repositories", f.permissions)
	mux.HandleFunc("GET "+repo, f.getRepo)
	mux.HandleFunc("POST "+repo, f.createRepo)
	mux.HandleFunc("DELETE "+repo, f.deleteRepo)
	mux.HandleFunc("POST "+repo+"/deploy-keys", f.createKey)
	mux.HandleFunc("GET "+repo+"/deploy-keys", f.listKeys)
	mux.HandleFunc("DELETE "+repo+"/deploy-keys/{id}", f.deleteKey)
	mux.HandleFunc("POST "+repo+"/hooks", f.createHook)
	mux.HandleFunc("GET "+repo+"/hooks", f.listHooks)
	mux.HandleFunc("DELETE "+repo+"/hooks/{id}", f.deleteHook)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		loc, status := f.redirect, f.redirectStatus
		f.redirect, f.redirectStatus = "", 0
		f.mu.Unlock()
		if loc != "" {
			http.Redirect(w, r, loc, status)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// fail answers the way Bitbucket does: {"type":"error","error":{"message":…}}.
// The message is never anything the caller sent.
func (f *fixture) fail(w http.ResponseWriter, status int, message string) {
	f.ok(w, status, map[string]any{"type": "error", "error": map[string]string{"message": message}})
}

func (f *fixture) ok(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// user gates every endpoint. Bitbucket Cloud reads an access token as Bearer.
func (f *fixture) user(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") == "Bearer "+goodToken {
		return true
	}
	f.fail(w, http.StatusUnauthorized, "Access token expired or invalid.")
	return false
}

func (f *fixture) getUser(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	f.ok(w, http.StatusOK, map[string]any{
		"type":         "user",
		"username":     login,
		"display_name": "Octo Tau",
		"uuid":         userUUID,
		"account_id":   "557058:0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
		"links":        map[string]any{"html": map[string]string{"href": "https://" + f.host + "/" + login + "/"}},
	})
}

func (f *fixture) repoBody(r *fRepo) map[string]any {
	full := r.owner + "/" + r.name
	return map[string]any{
		"type":       "repository",
		"full_name":  full,
		"name":       r.name,
		"slug":       r.name,
		"uuid":       "{0a1b2c3d-" + r.name + "}",
		"scm":        "git",
		"is_private": r.private,
		"mainbranch": map[string]string{"type": "branch", "name": r.defaultBranch},
		"links": map[string]any{
			"html": map[string]string{"href": "https://" + f.host + "/" + full},
			"clone": []map[string]string{
				{"name": "https", "href": "https://" + login + "@" + f.host + "/" + full + ".git"},
				{"name": "ssh", "href": "ssh://git@" + f.host + "/" + full + ".git"},
			},
		},
	}
}

// page is Bitbucket's paged envelope, clamped to fixturePageLen, with a "next"
// link spelled the way Bitbucket spells one: the same request, page+1.
func (f *fixture) page(w http.ResponseWriter, r *http.Request, all []any) {
	n, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || n < 1 {
		n = 1
	}
	start := min((n-1)*fixturePageLen, len(all))
	end := min(start+fixturePageLen, len(all))
	body := map[string]any{"pagelen": fixturePageLen, "page": n, "size": len(all), "values": all[start:end]}
	if end < len(all) {
		q := r.URL.Query()
		q.Set("page", strconv.Itoa(n+1))
		body["next"] = (&url.URL{Scheme: "https", Host: f.host, Path: r.URL.Path, RawQuery: q.Encode()}).String()
	}
	f.ok(w, http.StatusOK, body)
}

func (f *fixture) lookup(owner, name string) (*fRepo, bool) {
	full := owner + "/" + name
	if to, moved := f.renames[full]; moved {
		full = to
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repos[full]
	return repo, ok
}

func (f *fixture) getRepo(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	repo, ok := f.lookup(r.PathValue("owner"), r.PathValue("name"))
	if !ok {
		f.fail(w, http.StatusNotFound, "Repository not found")
		return
	}
	f.mu.Lock()
	body := f.repoBody(repo)
	f.mu.Unlock()
	f.ok(w, http.StatusOK, body)
}

func (f *fixture) listRepos(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	if r.URL.Query().Get("role") != "member" {
		// Without role, /2.0/repositories is every public repository on
		// Bitbucket, which is not what a credential "can reach".
		f.fail(w, http.StatusBadRequest, "fixture: a listing without role=member lists the world")
		return
	}
	f.mu.Lock()
	var out []any
	for _, full := range sortedKeys(f.repos) {
		if repo := f.repos[full]; repo.perm != "" {
			out = append(out, f.repoBody(repo))
		}
	}
	f.mu.Unlock()
	f.page(w, r, out)
}

// permissions answers /user/permissions/repositories?q=repository.full_name="…",
// the only filter this fixture understands.
func (f *fixture) permissions(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	q := r.URL.Query().Get("q")
	full, ok := strings.CutPrefix(q, `repository.full_name="`)
	full, closed := strings.CutSuffix(full, `"`)
	if !ok || !closed {
		f.fail(w, http.StatusBadRequest, "fixture: unsupported filter")
		return
	}
	var out []any
	f.mu.Lock()
	if repo, ok := f.repos[full]; ok && repo.perm != "" {
		out = append(out, map[string]any{
			"type":       "repository_permission",
			"permission": repo.perm,
			"user":       map[string]string{"username": login, "uuid": userUUID},
			"repository": map[string]any{"type": "repository", "full_name": full, "name": repo.name},
		})
	}
	f.mu.Unlock()
	f.page(w, r, out)
}

func (f *fixture) createRepo(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	owner, name := r.PathValue("owner"), r.PathValue("name")
	if owner != namespace && owner != login {
		f.fail(w, http.StatusForbidden, "You do not have permission to create repositories in this workspace.")
		return
	}
	var body struct {
		SCM       string `json:"scm"`
		IsPrivate bool   `json:"is_private"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SCM != "git" {
		f.fail(w, http.StatusBadRequest, "scm: a git repository is the only kind")
		return
	}
	full := owner + "/" + name
	f.mu.Lock()
	if _, taken := f.repos[full]; taken {
		f.mu.Unlock()
		f.fail(w, http.StatusBadRequest, "Repository with this Slug and Owner already exists.")
		return
	}
	f.seed(owner, name, body.IsPrivate, "admin")
	created := f.repoBody(f.repos[full])
	f.mu.Unlock()
	f.ok(w, http.StatusOK, created)
}

func (f *fixture) deleteRepo(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	full := r.PathValue("owner") + "/" + r.PathValue("name")
	f.mu.Lock()
	repo, ok := f.repos[full]
	if ok && repo.perm == "admin" {
		delete(f.repos, full)
	}
	f.mu.Unlock()
	switch {
	case !ok:
		f.fail(w, http.StatusNotFound, "Repository not found")
	case repo.perm != "admin":
		f.fail(w, http.StatusForbidden, "You do not have admin access to this repository.")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// adminRepo is the guard every sub-resource shares.
func (f *fixture) adminRepo(w http.ResponseWriter, r *http.Request) (string, bool) {
	full := r.PathValue("owner") + "/" + r.PathValue("name")
	f.mu.Lock()
	repo, ok := f.repos[full]
	f.mu.Unlock()
	switch {
	case !ok:
		f.fail(w, http.StatusNotFound, "Repository not found")
		return "", false
	case repo.perm != "admin":
		f.fail(w, http.StatusForbidden, "You do not have admin access to this repository.")
		return "", false
	}
	return full, true
}

func (f *fixture) nextID() int64 {
	f.seq++
	return f.seq
}

// strictBody decodes exactly the fields Bitbucket defines, so a driver sending
// one it does not — a read_only, say — is refused rather than ignored.
func strictBody(r *http.Request, into any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(into)
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
		Key   string `json:"key"`
		Label string `json:"label"`
	}
	if err := strictBody(r, &body); err != nil || body.Key == "" {
		f.fail(w, http.StatusBadRequest, "key: invalid")
		return
	}
	f.mu.Lock()
	id := f.nextID()
	f.keys[strconv.FormatInt(id, 10)] = &fKey{full: full, label: body.Label, pub: body.Key}
	f.mu.Unlock()
	f.ok(w, http.StatusOK, map[string]any{"type": "deploy_key", "id": id, "key": body.Key, "label": body.Label})
}

func (f *fixture) listKeys(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	full, ok := f.adminRepo(w, r)
	if !ok {
		return
	}
	var out []any
	f.mu.Lock()
	for _, id := range sortedKeys(f.keys) {
		if k := f.keys[id]; k.full == full {
			n, err := strconv.ParseInt(id, 10, 64)
			if err != nil {
				panic(err)
			}
			out = append(out, map[string]any{"type": "deploy_key", "id": n, "key": k.pub, "label": k.label})
		}
	}
	f.mu.Unlock()
	f.page(w, r, out)
}

func (f *fixture) deleteKey(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	full, ok := f.adminRepo(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	f.mu.Lock()
	k, ok := f.keys[id]
	ok = ok && k.full == full
	if ok {
		delete(f.keys, id)
	}
	f.mu.Unlock()
	if !ok {
		f.fail(w, http.StatusNotFound, "Deploy key not found")
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
		Description string   `json:"description"`
		URL         string   `json:"url"`
		Active      bool     `json:"active"`
		Events      []string `json:"events"`
		Secret      string   `json:"secret"`
	}
	if err := strictBody(r, &body); err != nil || body.URL == "" || !slices.Equal(body.Events, []string{"repo:push"}) {
		f.fail(w, http.StatusBadRequest, "url: invalid")
		return
	}
	f.mu.Lock()
	uuid := fmt.Sprintf("{6a2f%04d-1b2c-4d3e-8f90-a1b2c3d4e5f6}", f.nextID()%10000)
	f.hooks[uuid] = &fHook{full: full, callback: body.URL, wire: body.Secret}
	f.lastWire = body.Secret
	f.mu.Unlock()
	f.ok(w, http.StatusCreated, map[string]any{
		"type": "webhook_subscription", "uuid": uuid, "url": body.URL, "description": body.Description,
		"active": body.Active, "events": body.Events, "secret_set": body.Secret != "",
	})
}

func (f *fixture) listHooks(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	full, ok := f.adminRepo(w, r)
	if !ok {
		return
	}
	var out []any
	f.mu.Lock()
	for _, id := range sortedKeys(f.hooks) {
		if h := f.hooks[id]; h.full == full {
			out = append(out, map[string]any{"type": "webhook_subscription", "uuid": id, "url": h.callback, "active": true, "events": []string{"repo:push"}})
		}
	}
	f.mu.Unlock()
	f.page(w, r, out)
}

func (f *fixture) deleteHook(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	full, ok := f.adminRepo(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	f.mu.Lock()
	h, ok := f.hooks[id]
	ok = ok && h.full == full
	if ok {
		delete(f.hooks, id)
	}
	f.mu.Unlock()
	if !ok {
		f.fail(w, http.StatusNotFound, "Webhook not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
