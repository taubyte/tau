package bitbucketserver_test

// An httptest server replaying Bitbucket Data Center's REST 1.0 shapes:
// clamped pages, an upper-case project key, clone links with the caller's
// username and the ssh port, 401 naming an AuthorisationException for a missing
// permission, 404 for a hidden repository, 409 for a taken slug, and access
// keys carrying a permission.

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
	goodToken = "bbs-tok-5e1d9a7c3b2f"
	badToken  = "bbs-tok-revoked-4c8e1a"

	login  = "octo-tau"
	userID = 1042

	project = "acme"

	api  = "/rest/api/1.0/"
	keys = "/rest/keys/1.0/"

	// fixturePageLimit is far below what the driver asks, so every listing in
	// the suite really is paged.
	fixturePageLimit = 2

	sshPort = "7999"
)

type fRepo struct {
	project, slug string
	public        bool
	perm          string // "admin", "write", "read", or "" for no grant
}

func (r *fRepo) full() string { return r.project + "/" + r.slug }

type fHook struct {
	full, callback, wire string
}

type fKey struct {
	full, label, text, perm string
}

type fixture struct {
	srv  *httptest.Server
	rt   *breakableRT
	host string

	renames map[string]string

	mu             sync.Mutex
	repos          map[string]*fRepo
	hooks          map[string]*fHook
	keys           map[string]*fKey
	seq            int64
	lastWire       string
	lastKeyPerm    string
	redirect       string
	redirectStatus int
	// emptyDefault answers no default branch for every repository.
	emptyDefault bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		repos: map[string]*fRepo{},
		hooks: map[string]*fHook{},
		keys:  map[string]*fKey{},
		seq:   1000,
	}
	f.srv = httptest.NewTLSServer(f.handler())
	t.Cleanup(f.srv.Close)
	f.host = strings.TrimPrefix(f.srv.URL, "https://")
	f.rt = &breakableRT{base: f.srv.Client().Transport}

	f.seed(project, "pay", false, "write")     // Writable
	f.seed(project, "public", true, "")        // ReadOnly: public, no grant
	f.seed(project, "site", false, "admin")    // Admin
	f.seed(project, "scratch", false, "admin") // Deletable
	// stranger/secret exists and grants nothing: Bitbucket answers it 404,
	// the same as acme/nope, which does not exist.
	f.seed("stranger", "secret", false, "")
	f.renames = map[string]string{"acme/billing": "acme/pay"}
	return f
}

func (f *fixture) seed(p, slug string, public bool, perm string) {
	f.repos[p+"/"+slug] = &fRepo{project: p, slug: slug, public: public, perm: perm}
}

func (f *fixture) Host() string                 { return f.host }
func (f *fixture) Transport() http.RoundTripper { return f.rt }

func (f *fixture) sshHost() string {
	name, _, _ := strings.Cut(f.host, ":")
	return name + ":" + sshPort
}

func (f *fixture) key(t *testing.T, full string) repokey.Key {
	t.Helper()
	k, err := repokey.Parse("https://" + f.host + "/" + full)
	if err != nil {
		t.Fatalf("fixture: %q is not a key: %v", full, err)
	}
	return k
}

func (f *fixture) Requests() []forgetest.Request { return f.rt.drain() }
func (f *fixture) Break()                        { f.rt.broken.Store(true) }
func (f *fixture) Unbreak()                      { f.rt.broken.Store(false) }

func (f *fixture) RedirectOnce(loc string, status int) {
	f.mu.Lock()
	f.redirect, f.redirectStatus = loc, status
	f.mu.Unlock()
}

// LastDeployKeyReadOnly reads the permission the driver actually sent with the
// last access key.
func (f *fixture) LastDeployKeyReadOnly() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastKeyPerm == "REPO_READ"
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

func (f *fixture) handler() http.Handler {
	mux := http.NewServeMux()
	repo := "projects/{project}/repos/{slug}"

	mux.HandleFunc("GET /plugins/servlet/applinks/whoami", f.whoami)
	mux.HandleFunc("GET "+api+"users/{slug}", f.getUser)
	mux.HandleFunc("GET "+api+"repos", f.listRepos)
	mux.HandleFunc("GET "+api+repo, f.getRepo)
	mux.HandleFunc("GET "+api+repo+"/branches/default", f.defaultBranch)
	mux.HandleFunc("POST "+api+"projects/{project}/repos", f.createRepo)
	mux.HandleFunc("DELETE "+api+repo, f.deleteRepo)
	mux.HandleFunc("POST "+keys+repo+"/ssh", f.createKey)
	mux.HandleFunc("GET "+keys+repo+"/ssh", f.listKeys)
	mux.HandleFunc("DELETE "+keys+repo+"/ssh/{id}", f.deleteKey)
	mux.HandleFunc("POST "+api+repo+"/webhooks", f.createHook)
	mux.HandleFunc("GET "+api+repo+"/webhooks", f.listHooks)
	mux.HandleFunc("DELETE "+api+repo+"/webhooks/{id}", f.deleteHook)

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

// fail answers the way Bitbucket Server does: {"errors":[{message, exceptionName}]}.
func (f *fixture) fail(w http.ResponseWriter, status int, exception, message string) {
	f.ok(w, status, map[string]any{"errors": []map[string]any{{"context": nil, "message": message, "exceptionName": exception}}})
}

func (f *fixture) forbid(w http.ResponseWriter) {
	f.fail(w, http.StatusUnauthorized, "com.atlassian.bitbucket.AuthorisationException", "You are not permitted to access this resource")
}

func (f *fixture) ok(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func (f *fixture) user(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") == "Bearer "+goodToken {
		return true
	}
	f.fail(w, http.StatusUnauthorized, "com.atlassian.bitbucket.auth.IncorrectPasswordAuthenticationException", "Authentication failed. Please check your credentials and try again.")
	return false
}

func (f *fixture) whoami(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, login)
}

func (f *fixture) getUser(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	if r.PathValue("slug") != login {
		f.fail(w, http.StatusNotFound, "com.atlassian.bitbucket.user.NoSuchUserException", "User does not exist")
		return
	}
	f.ok(w, http.StatusOK, map[string]any{
		"name": login, "slug": login, "id": userID, "emailAddress": "octo@example.com",
		"displayName": "Octo Tau", "active": true, "type": "NORMAL",
	})
}

func (f *fixture) repoBody(r *fRepo) map[string]any {
	upper := strings.ToUpper(r.project)
	if strings.HasPrefix(r.project, "~") {
		upper = r.project
	}
	return map[string]any{
		"slug": r.slug, "id": 7, "name": r.slug, "scmId": "git", "state": "AVAILABLE",
		"public":  r.public,
		"project": map[string]any{"key": upper, "id": 3, "name": upper, "type": "NORMAL"},
		"links": map[string]any{
			"clone": []map[string]string{
				{"name": "http", "href": "https://" + login + "@" + f.host + "/scm/" + upper + "/" + r.slug + ".git"},
				{"name": "ssh", "href": "ssh://git@" + f.sshHost() + "/" + upper + "/" + r.slug + ".git"},
			},
			"self": []map[string]string{{"href": "https://" + f.host + "/projects/" + upper + "/repos/" + r.slug + "/browse"}},
		},
	}
}

// page is Bitbucket Server's paged envelope, clamped to fixturePageLimit.
func (f *fixture) page(w http.ResponseWriter, r *http.Request, all []any) {
	start, err := strconv.Atoi(r.URL.Query().Get("start"))
	if err != nil || start < 0 {
		start = 0
	}
	start = min(start, len(all))
	end := min(start+fixturePageLimit, len(all))
	body := map[string]any{"size": end - start, "limit": fixturePageLimit, "start": start, "values": all[start:end], "isLastPage": end == len(all)}
	if end < len(all) {
		body["nextPageStart"] = end
	}
	f.ok(w, http.StatusOK, body)
}

func (f *fixture) lookup(r *http.Request) (*fRepo, bool) {
	full := strings.ToLower(r.PathValue("project")) + "/" + r.PathValue("slug")
	if to, moved := f.renames[full]; moved {
		full = to
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repos[full]
	if ok && repo.perm == "" && !repo.public {
		// Bitbucket does not say a repository it will not show exists.
		return nil, false
	}
	return repo, ok
}

func (f *fixture) getRepo(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	repo, ok := f.lookup(r)
	if !ok {
		f.fail(w, http.StatusNotFound, "com.atlassian.bitbucket.repository.NoSuchRepositoryException", "Repository does not exist")
		return
	}
	f.mu.Lock()
	body := f.repoBody(repo)
	f.mu.Unlock()
	f.ok(w, http.StatusOK, body)
}

func (f *fixture) defaultBranch(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	f.mu.Lock()
	empty := f.emptyDefault
	f.mu.Unlock()
	if _, ok := f.lookup(r); !ok || empty {
		f.fail(w, http.StatusNotFound, "com.atlassian.bitbucket.repository.NoDefaultBranchException", "Repository has no default branch")
		return
	}
	f.ok(w, http.StatusOK, map[string]any{"id": "refs/heads/main", "displayId": "main", "type": "BRANCH", "isDefault": true})
}

var rank = map[string]int{"REPO_READ": 1, "REPO_WRITE": 2, "REPO_ADMIN": 3}

func (r *fRepo) holds(perm string) bool {
	have := map[string]int{"admin": 3, "write": 2, "read": 1}[r.perm]
	if r.public {
		have = max(have, 1)
	}
	return have >= rank[perm]
}

func (f *fixture) listRepos(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	q := r.URL.Query()
	perm := q.Get("permission")
	if perm == "" {
		perm = "REPO_READ"
	}
	if _, ok := rank[perm]; !ok {
		f.fail(w, http.StatusBadRequest, "com.atlassian.bitbucket.validation.ArgumentValidationException", "permission: unknown")
		return
	}
	name := strings.ToLower(strings.TrimSpace(q.Get("name")))
	f.mu.Lock()
	var out []any
	for _, full := range sortedKeys(f.repos) {
		repo := f.repos[full]
		if repo.holds(perm) && strings.Contains(repo.slug, name) {
			out = append(out, f.repoBody(repo))
		}
	}
	f.mu.Unlock()
	f.page(w, r, out)
}

func (f *fixture) createRepo(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	p := strings.ToLower(r.PathValue("project"))
	if p != project && p != "~"+login {
		f.forbid(w)
		return
	}
	type requestBody struct {
		Name        string `json:"name"`
		SCMID       string `json:"scmId"`
		Description string `json:"description"`
		Public      bool   `json:"public"`
	}
	var body requestBody
	if err := strictBody(r, &body); err != nil || body.SCMID != "git" || body.Name == "" {
		f.fail(w, http.StatusBadRequest, "com.atlassian.bitbucket.validation.ArgumentValidationException", "name: required")
		return
	}
	slug := strings.ToLower(body.Name)
	f.mu.Lock()
	if _, taken := f.repos[p+"/"+slug]; taken {
		f.mu.Unlock()
		f.fail(w, http.StatusConflict, "com.atlassian.bitbucket.repository.DuplicateRepositoryNameException", "This repository name is already taken.")
		return
	}
	f.seed(p, slug, body.Public, "admin")
	created := f.repoBody(f.repos[p+"/"+slug])
	f.mu.Unlock()
	f.ok(w, http.StatusCreated, created)
}

func (f *fixture) adminRepo(w http.ResponseWriter, r *http.Request) (string, bool) {
	repo, ok := f.lookup(r)
	switch {
	case !ok:
		f.fail(w, http.StatusNotFound, "com.atlassian.bitbucket.repository.NoSuchRepositoryException", "Repository does not exist")
		return "", false
	case repo.perm != "admin":
		f.forbid(w)
		return "", false
	}
	return repo.full(), true
}

func (f *fixture) deleteRepo(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	full, ok := f.adminRepo(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	delete(f.repos, full)
	f.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

func strictBody(r *http.Request, into any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(into)
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
	type keyBody struct {
		Text  string `json:"text"`
		Label string `json:"label"`
	}
	type requestBody struct {
		Key        keyBody `json:"key"`
		Permission string  `json:"permission"`
	}
	var body requestBody
	if err := strictBody(r, &body); err != nil || body.Key.Text == "" || (body.Permission != "REPO_READ" && body.Permission != "REPO_WRITE") {
		f.fail(w, http.StatusBadRequest, "com.atlassian.bitbucket.validation.ArgumentValidationException", "key: invalid")
		return
	}
	f.mu.Lock()
	id := f.nextID()
	f.keys[strconv.FormatInt(id, 10)] = &fKey{full: full, label: body.Key.Label, text: body.Key.Text, perm: body.Permission}
	f.lastKeyPerm = body.Permission
	f.mu.Unlock()
	f.ok(w, http.StatusCreated, f.keyBody(id, body.Key.Text, body.Key.Label, body.Permission))
}

func (f *fixture) keyBody(id int64, text, label, perm string) map[string]any {
	return map[string]any{"key": map[string]any{"id": id, "text": text, "label": label}, "permission": perm}
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
			out = append(out, f.keyBody(n, k.text, k.label, k.perm))
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
		f.fail(w, http.StatusNotFound, "com.atlassian.bitbucket.ssh.NoSuchSshKeyException", "Key does not exist")
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
	type requestBody struct {
		Name          string            `json:"name"`
		URL           string            `json:"url"`
		Active        bool              `json:"active"`
		Events        []string          `json:"events"`
		Configuration map[string]string `json:"configuration"`
	}
	var body requestBody
	if err := strictBody(r, &body); err != nil || body.URL == "" || !slices.Equal(body.Events, []string{"repo:refs_changed"}) {
		f.fail(w, http.StatusBadRequest, "com.atlassian.bitbucket.validation.ArgumentValidationException", "url: invalid")
		return
	}
	f.mu.Lock()
	id := f.nextID()
	f.hooks[strconv.FormatInt(id, 10)] = &fHook{full: full, callback: body.URL, wire: body.Configuration["secret"]}
	f.lastWire = body.Configuration["secret"]
	f.mu.Unlock()
	f.ok(w, http.StatusCreated, map[string]any{"id": id, "name": body.Name, "url": body.URL, "active": body.Active, "events": body.Events})
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
			n, err := strconv.ParseInt(id, 10, 64)
			if err != nil {
				panic(err)
			}
			out = append(out, map[string]any{"id": n, "name": "tau", "url": h.callback, "active": true, "events": []string{"repo:refs_changed"}})
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
		f.fail(w, http.StatusNotFound, "com.atlassian.bitbucket.webhook.NoSuchWebhookException", "Webhook does not exist")
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
