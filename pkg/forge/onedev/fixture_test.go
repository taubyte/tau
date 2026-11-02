package onedev_test

// An httptest server written from what a real OneDev 16.9 answered (the live
// suite runs the driver against one): an id lookup 404 for a project absent or
// invisible and 401 to an anonymous caller; 403 per privilege; 204 for an empty
// repository's default branch; a receive-pack advertisement 200 to a pusher and
// 403 otherwise; 500 for a duplicate name.

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

	"github.com/taubyte/tau/pkg/forge/forgetest"
	"github.com/taubyte/tau/pkg/git/repokey"
)

const (
	goodToken = "od-tok-4b1c9e7f2a60"
	badToken  = "od-tok-revoked-91d3"

	login  = "tau-dev"
	userID = 3
)

type grant int

const (
	hidden    grant = iota // not visible at all
	noCode                 // visible, code not readable (an issue reporter)
	readCode               // Code Reader
	writeCode              // Code Writer
	manage                 // Project Owner
)

type fProject struct {
	id       int64
	parent   int64
	name     string
	path     string
	grant    grant
	public   bool // readable with no credential
	empty    bool // no branch yet
	settings map[string]json.RawMessage
}

type fixture struct {
	srv  *httptest.Server
	rt   *breakableRT
	host string

	// renames are paths this OneDev resolves to a project that no longer has
	// them. Written before the server is handed out.
	renames map[string]string

	mu             sync.Mutex
	projects       map[int64]*fProject
	seq            int64
	redirect       string
	redirectStatus int
	// listPage overrides the page size the listing answers with, when set.
	listPage int
	// answer, when set, sees every request first and may answer it itself:
	// how a test makes this OneDev say something a real one rarely does.
	answer func(w http.ResponseWriter, r *http.Request) bool
}

// Answer installs a per-request override for the rest of the test.
func (f *fixture) Answer(fn func(w http.ResponseWriter, r *http.Request) bool) {
	f.mu.Lock()
	f.answer = fn
	f.mu.Unlock()
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{projects: map[int64]*fProject{}}
	f.srv = httptest.NewTLSServer(f.handler())
	t.Cleanup(f.srv.Close)
	f.host = strings.TrimPrefix(f.srv.URL, "https://")
	f.rt = &breakableRT{base: f.srv.Client().Transport}

	acme := f.seed(0, "acme", manage, false)
	f.seed(acme, "pay", writeCode, false)  // Writable
	f.seed(acme, "public", readCode, true) // ReadOnly
	f.seed(acme, "site", manage, false)    // Admin
	f.seed(acme, "scratch", manage, false) // Deletable
	f.seed(acme, "issues", noCode, false)  // visible, no code
	team := f.seed(acme, "team", manage, false)
	f.seed(team, "widget", manage, false) // Deep
	stranger := f.seed(0, "stranger", readCode, false)
	f.seed(stranger, "secret", hidden, false) // NoAccess
	f.renames = map[string]string{"acme/billing": "acme/pay"}
	return f
}

func (f *fixture) seed(parent int64, name string, g grant, public bool) int64 {
	f.seq++
	p := &fProject{
		id: f.seq, parent: parent, name: name, grant: g, public: public,
		settings: map[string]json.RawMessage{
			// A field the driver must carry back untouched on every save.
			"branchProtections": json.RawMessage(`[{"enabled":true,"branches":"main"}]`),
			"webHooks":          json.RawMessage(`[]`),
		},
	}
	p.path = name
	if parent != 0 {
		p.path = f.projects[parent].path + "/" + name
	}
	f.projects[p.id] = p
	return p.id
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
func (f *fixture) Break()                        { f.rt.broken.Store(true) }
func (f *fixture) Unbreak()                      { f.rt.broken.Store(false) }

func (f *fixture) RedirectOnce(loc string, status int) {
	f.mu.Lock()
	f.redirect, f.redirectStatus = loc, status
	f.mu.Unlock()
}

// byPath is the project at path, or nil. Callers hold f.mu.
func (f *fixture) byPath(path string) *fProject {
	for _, p := range f.projects {
		if p.path == path {
			return p
		}
	}
	return nil
}

type breakableRT struct {
	base   http.RoundTripper
	broken atomic.Bool
	mu     sync.Mutex
	sent   []forgetest.Request
	// all is every request ever composed, never drained: what the sweep reads.
	all []forgetest.Request
}

func (b *breakableRT) everything() []forgetest.Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]forgetest.Request(nil), b.all...)
}

func (b *breakableRT) drain() []forgetest.Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.sent
	b.sent = nil
	return out
}

// RoundTrip records before it decides, so a request the wire swallowed is
// still one the driver composed — which is how the suite learns where a
// destructive operation would have gone without letting it go there.
func (b *breakableRT) RoundTrip(r *http.Request) (*http.Response, error) {
	b.mu.Lock()
	req := forgetest.Request{Method: r.Method, URL: r.URL.String(), Header: r.Header.Clone()}
	b.sent = append(b.sent, req)
	b.all = append(b.all, req)
	b.mu.Unlock()
	if b.broken.Load() {
		return nil, fmt.Errorf("fixture: the forge is unreachable")
	}
	return b.base.RoundTrip(r)
}

func (f *fixture) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /~api/users/me", f.me)
	mux.HandleFunc("GET /~api/projects", f.list)
	mux.HandleFunc("POST /~api/projects", f.create)
	mux.HandleFunc("DELETE /~api/projects/{id}", f.delete)
	mux.HandleFunc("GET /~api/projects/{id}/clone-url", f.cloneURL)
	mux.HandleFunc("GET /~api/projects/{id}/setting", f.getSetting)
	mux.HandleFunc("GET /~api/repositories/{id}/default-branch", f.defaultBranch)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		loc, status := f.redirect, f.redirectStatus
		f.redirect, f.redirectStatus = "", 0
		answer := f.answer
		f.mu.Unlock()
		if answer != nil && answer(w, r) {
			return
		}
		if loc != "" {
			http.Redirect(w, r, loc, status)
			return
		}
		// git's endpoint is the project path with .git, which a mux pattern
		// cannot spell with a wildcard in the middle.
		// So is the id lookup, whose path wildcard overlaps /projects/{id}/....
		if p, ok := strings.CutPrefix(r.URL.Path, "/~api/projects/ids/"); ok && r.Method == http.MethodGet {
			f.ids(w, r, p)
			return
		}
		if p, ok := strings.CutSuffix(r.URL.Path, ".git/info/refs"); ok && r.Method == http.MethodGet {
			f.advertise(w, r, strings.TrimPrefix(p, "/"))
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (f *fixture) fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(status)
	fmt.Fprint(w, message)
}

func (f *fixture) ok(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}

// who is the caller: 1 the fixture user, 0 anonymous, -1 refused (answered).
func (f *fixture) who(w http.ResponseWriter, r *http.Request) int {
	h := r.Header.Get("Authorization")
	switch {
	case h == "":
		return 0
	case h == "Bearer "+goodToken:
		return 1
	}
	f.fail(w, http.StatusUnauthorized, "Invalid account or incorrect credentials")
	return -1
}

func (f *fixture) visible(p *fProject, who int) bool {
	if who == 0 {
		return p.public
	}
	return p.grant != hidden || p.public
}

func (f *fixture) me(w http.ResponseWriter, r *http.Request) {
	switch f.who(w, r) {
	case 0:
		f.fail(w, http.StatusUnauthorized, "Not authenticated")
	case 1:
		f.ok(w, map[string]any{"id": userID, "name": login, "type": "ORDINARY", "disabled": false})
	}
}

func (f *fixture) ids(w http.ResponseWriter, r *http.Request, path string) {
	who := f.who(w, r)
	if who < 0 {
		return
	}
	if to, moved := f.renames[path]; moved {
		path = to
	}
	f.mu.Lock()
	p := f.byPath(path)
	f.mu.Unlock()
	switch {
	case p != nil && f.visible(p, who):
		f.ok(w, p.id)
	case who == 0:
		f.fail(w, http.StatusUnauthorized, "Not authenticated")
	default:
		f.fail(w, http.StatusNotFound, "Project not found or inaccessible: "+path)
	}
}

// project is the project {id} names, answered 403 when the caller may not see
// it, exactly as OneDev answers a numeric id it will not describe.
func (f *fixture) project(w http.ResponseWriter, r *http.Request) (*fProject, int, bool) {
	who := f.who(w, r)
	if who < 0 {
		return nil, 0, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	f.mu.Lock()
	p := f.projects[id]
	f.mu.Unlock()
	switch {
	case err != nil:
		f.fail(w, http.StatusNotFound, "Not found")
	case p == nil:
		f.fail(w, http.StatusNotFound, "No row with the given identifier exists")
	case !f.visible(p, who):
		f.fail(w, http.StatusForbidden, "Not authorized")
	default:
		return p, who, true
	}
	return nil, 0, false
}

func (f *fixture) cloneURL(w http.ResponseWriter, r *http.Request) {
	p, _, ok := f.project(w, r)
	if !ok {
		return
	}
	f.ok(w, map[string]string{
		"http": "https://" + f.host + "/" + p.path + ".git",
		"ssh":  "ssh://" + f.host + "/" + p.path + ".git",
	})
}

func (f *fixture) defaultBranch(w http.ResponseWriter, r *http.Request) {
	p, who, ok := f.project(w, r)
	if !ok {
		return
	}
	if !(p.public || (who == 1 && p.grant >= readCode)) {
		f.fail(w, http.StatusForbidden, "Not authorized")
		return
	}
	if p.empty {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "main")
}

func (f *fixture) manager(w http.ResponseWriter, r *http.Request) (*fProject, bool) {
	p, who, ok := f.project(w, r)
	if !ok {
		return nil, false
	}
	if who != 1 || p.grant != manage {
		f.fail(w, http.StatusForbidden, "Not authorized")
		return nil, false
	}
	return p, true
}

func (f *fixture) getSetting(w http.ResponseWriter, r *http.Request) {
	p, ok := f.manager(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	out := map[string]json.RawMessage{}
	for k, v := range p.settings {
		out[k] = v
	}
	f.mu.Unlock()
	f.ok(w, out)
}

func (f *fixture) list(w http.ResponseWriter, r *http.Request) {
	who := f.who(w, r)
	if who < 0 {
		return
	}
	offset, err1 := strconv.Atoi(r.URL.Query().Get("offset"))
	count, err2 := strconv.Atoi(r.URL.Query().Get("count"))
	if err1 != nil || err2 != nil || count > 100 {
		f.fail(w, http.StatusNotAcceptable, "Count should not exceed 100")
		return
	}
	f.mu.Lock()
	if f.listPage > 0 {
		count = f.listPage
	}
	ids := make([]int64, 0, len(f.projects))
	for id, p := range f.projects {
		if f.visible(p, who) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	out := []map[string]any{}
	for i := offset; i < len(ids) && i < offset+count; i++ {
		p := f.projects[ids[i]]
		out = append(out, map[string]any{"id": p.id, "parentId": p.parent, "name": p.name, "path": p.path})
	}
	f.mu.Unlock()
	f.ok(w, out)
}

func (f *fixture) create(w http.ResponseWriter, r *http.Request) {
	if f.who(w, r) != 1 {
		f.fail(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	type requestBody struct {
		Name         string           `json:"name"`
		ParentID     int64            `json:"parentId"`
		GitPack      *json.RawMessage `json:"gitPackConfig"`
		CodeIndexing *json.RawMessage `json:"codeIndexingSetting"`
		Description  string           `json:"description"`
	}
	var body requestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.GitPack == nil || body.CodeIndexing == nil {
		f.fail(w, http.StatusBadRequest, "createProject.arg0.gitPackConfig: must not be null")
		return
	}
	f.mu.Lock()
	parent := f.projects[body.ParentID]
	switch {
	case parent == nil || parent.grant != manage:
		f.mu.Unlock()
		f.fail(w, http.StatusForbidden, "Not authorized to create project under this parent")
		return
	case f.byPath(parent.path+"/"+body.Name) != nil:
		f.mu.Unlock()
		f.fail(w, http.StatusInternalServerError, "Name '"+body.Name+"' is already used by another project")
		return
	}
	id := f.seed(body.ParentID, body.Name, manage, false)
	f.projects[id].empty = false
	f.mu.Unlock()
	f.ok(w, id)
}

func (f *fixture) delete(w http.ResponseWriter, r *http.Request) {
	p, ok := f.manager(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	delete(f.projects, p.id)
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (f *fixture) advertise(w http.ResponseWriter, r *http.Request, path string) {
	who := f.who(w, r)
	if who < 0 {
		return
	}
	if r.URL.Query().Get("service") != "git-receive-pack" {
		f.fail(w, http.StatusBadRequest, "fixture serves only receive-pack advertisements")
		return
	}
	f.mu.Lock()
	p := f.byPath(path)
	f.mu.Unlock()
	switch {
	case who == 0:
		f.fail(w, http.StatusUnauthorized, "Not authenticated")
	case p == nil || !f.visible(p, who):
		f.fail(w, http.StatusNotFound, "Not found")
	case p.grant < writeCode:
		f.fail(w, http.StatusForbidden, "Not authorized")
	default:
		w.Header().Set("Content-Type", "application/x-git-receive-pack-advertisement")
		fmt.Fprint(w, "001f# service=git-receive-pack\n0000")
	}
}
