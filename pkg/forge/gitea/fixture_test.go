package gitea_test

// An httptest server serving the thirteen endpoints the driver calls in Gitea's
// shapes and status codes, including the redirect Gitea answers a membership
// question with. The container suite, tag forges, has a real one.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	goodToken = "gt-tok-9c4f1ae2d7b6"
	badToken  = "gt-tok-revoked-77b1e3"

	login  = "octo-tau"
	userID = 4711

	// memberToken is the DEPLOYMENT's credential, the only one the membership
	// endpoint answers definitively.
	memberToken = "gt-member-5e0b7d"

	namespace = "acme"
	member    = "alice"
	nonMember = "mallory"

	apiPrefix = "/api/v1/"
)

type perm struct{ admin, push, pull bool }

var (
	permAdmin = perm{admin: true, push: true, pull: true}
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

type fixture struct {
	srv  *httptest.Server
	rt   *breakableRT
	host string

	// renames are the repositories this Gitea answers for under a name they no
	// longer have. It is written once, before the server is handed out, and
	// read without the lock for that reason.
	renames map[string]string

	mu             sync.Mutex
	repos          map[string]*fRepo
	hooks          map[string]*fHook
	keys           map[string]*fKey
	seq            int64
	requests       []forgetest.Request
	lastWire       string
	lastRO         bool
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

	f.seed("acme", "pay", true, permPush)      // Writable
	f.seed("acme", "public", false, permPull)  // ReadOnly: readable, no grant
	f.seed("acme", "site", true, permAdmin)    // Admin
	f.seed("acme", "scratch", true, permAdmin) // Deletable
	// NoAccess (stranger/secret) and Missing (acme/nope) are deliberately not
	// seeded: Gitea hides a repository a credential may not see behind the same
	// 404 it gives one that does not exist, and pretending otherwise here would
	// test a forge nobody runs.

	// acme/billing was renamed to acme/pay, and this Gitea answers the old name with
	// the new repository. It resolves rather than redirects: the driver follows no
	// redirect, and this fixture asserts derivation.
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
func (f *fixture) Requests() []forgetest.Request { return f.rt.drain() }

// Break fails the next request AT THE TRANSPORT rather than answering it with a
// 500: an outage is not a status code, and the invariant being asserted is that
// a driver which never reached the forge cannot produce a verdict.
func (f *fixture) Break()   { f.rt.broken.Store(true) }
func (f *fixture) Unbreak() { f.rt.broken.Store(false) }

// The status is the suite's: 301, 302 and 303 make Go rewrite a write to a GET,
// 307 and 308 replay it.
func (f *fixture) RedirectOnce(loc string, status int) {
	f.mu.Lock()
	f.redirect, f.redirectStatus = loc, status
	f.mu.Unlock()
}

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

	mu   sync.Mutex
	sent []forgetest.Request
}

func (b *breakableRT) record(req forgetest.Request) {
	b.mu.Lock()
	b.sent = append(b.sent, req)
	b.mu.Unlock()
}

func (b *breakableRT) drain() []forgetest.Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.sent
	b.sent = nil
	return out
}

// RoundTrip records before it decides: Requests is what the subject sent, so a
// request the broken wire swallowed still shows where it would have gone. It is
// under the pin, so it sees the credential attached.
func (b *breakableRT) RoundTrip(r *http.Request) (*http.Response, error) {
	b.record(forgetest.Request{Method: r.Method, URL: r.URL.String(), Header: r.Header.Clone()})
	if b.broken.Load() {
		return nil, fmt.Errorf("fixture: the forge is unreachable")
	}
	return b.base.RoundTrip(r)
}

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
	mux.HandleFunc("GET "+apiPrefix+"repos/{owner}/{name}/keys", f.listKeys)
	mux.HandleFunc("GET "+apiPrefix+"repos/{owner}/{name}/hooks", f.listHooks)
	mux.HandleFunc("POST "+apiPrefix+"repos/{owner}/{name}/hooks", f.createHook)
	mux.HandleFunc("DELETE "+apiPrefix+"repos/{owner}/{name}/hooks/{id}", f.deleteHook)
	mux.HandleFunc("GET "+apiPrefix+"orgs/{org}/members/{user}", f.getMembership)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		loc, status := f.redirect, f.redirectStatus
		f.redirect, f.redirectStatus = "", 0
		f.mu.Unlock()
		if loc != "" {
			// A forge answering a 3xx to somewhere else. It is the whole reason
			// the credential is attached by a pinned transport rather than by
			// the driver: an http.Client follows this, and a RoundTripper that
			// sets a header sets it again on the next hop.
			http.Redirect(w, r, loc, status)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// fail answers the way Gitea does: a status and a JSON message. The message is
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

// bearer is the token from Gitea's "token <t>" header. Anything else — Bearer,
// basic — reads as no token, so a driver that sent another form is a 401 here.
func bearer(r *http.Request) string {
	t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "token ")
	if !ok {
		return ""
	}
	return t
}

func (f *fixture) user(w http.ResponseWriter, r *http.Request) bool {
	if bearer(r) == goodToken {
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
			"admin": r.perm.admin, "push": r.perm.push, "pull": r.perm.pull,
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
		// Gitea's answer for an organization this credential may not create in.
		f.fail(w, http.StatusForbidden, "Given user is not allowed to create repository in organization.")
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
		f.fail(w, http.StatusConflict, "The repository with the same name already exists.")
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
	f.ok(w, http.StatusCreated, map[string]any{"id": id, "read_only": body.ReadOnly})
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

func (f *fixture) listKeys(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	full, ok := f.adminRepo(w, r)
	if !ok {
		return
	}
	out := []map[string]any{}
	f.mu.Lock()
	for id, k := range f.keys {
		if k.full == full {
			n, _ := strconv.ParseInt(id, 10, 64)
			out = append(out, map[string]any{"id": n, "title": k.title, "key": k.pub})
		}
	}
	f.mu.Unlock()
	if r.URL.Query().Get("page") != "1" {
		out = nil
	}
	f.ok(w, http.StatusOK, out)
}

func (f *fixture) listHooks(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	full, ok := f.adminRepo(w, r)
	if !ok {
		return
	}
	out := []map[string]any{}
	f.mu.Lock()
	for id, h := range f.hooks {
		if h.full == full {
			n, _ := strconv.ParseInt(id, 10, 64)
			out = append(out, map[string]any{"id": n, "config": map[string]any{"url": h.callback}})
		}
	}
	f.mu.Unlock()
	if r.URL.Query().Get("page") != "1" {
		out = nil
	}
	f.ok(w, http.StatusOK, out)
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
		Type   string   `json:"type"`
		Active bool     `json:"active"`
		Events []string `json:"events"`
		Config struct {
			URL         string `json:"url"`
			ContentType string `json:"content_type"`
			Secret      string `json:"secret"`
		} `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Config.URL == "" || body.Type != "gitea" {
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

// getMembership is Gitea's: 204 for a member, 404 for anyone else — and for a
// token that may not list the organization's members, a redirect to the public
// list, which a driver that followed it would read as the whole membership.
func (f *fixture) getMembership(w http.ResponseWriter, r *http.Request) {
	switch bearer(r) {
	case memberToken:
	case goodToken:
		http.Redirect(w, r, apiPrefix+"orgs/"+r.PathValue("org")+"/public_members/"+r.PathValue("user"), http.StatusFound)
		return
	default:
		f.fail(w, http.StatusUnauthorized, "token is required")
		return
	}
	if r.PathValue("org") == namespace && r.PathValue("user") == member {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	f.fail(w, http.StatusNotFound, "Not Found")
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
