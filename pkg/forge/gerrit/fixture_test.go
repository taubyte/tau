package gerrit_test

// An httptest server in Gerrit's shapes: the )]}' guard, project names as one
// %2F-escaped segment, basic auth under /a/, plain-text errors, and the status
// codes measured against Gerrit 3.12.3. pkg/forge/live has a real one.

import (
	"encoding/base64"
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
	login    = "octo-tau"
	password = "gt-pw-9c4f1ae2d7b6"
	// goodToken is the raw credential, long and odd enough that finding its
	// password in a URL or an error string is unambiguous.
	goodToken = login + ":" + password
	badToken  = login + ":gt-pw-revoked-77b1e3"
	userID    = 1000042

	memberToken = "warden:gt-member-5e0b7d"

	namespace = "acme"
	member    = "alice"
	nonMember = "mallory"
)

type fProject struct {
	owned      bool
	head       string
	restricted bool
}

type fixture struct {
	srv  *httptest.Server
	rt   *breakableRT
	host string

	// renames is written once, before the server is handed out.
	renames map[string]string

	mu             sync.Mutex
	projects       map[string]*fProject
	redirect       string
	redirectStatus int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{projects: map[string]*fProject{}}
	f.srv = httptest.NewTLSServer(f.handler())
	t.Cleanup(f.srv.Close)
	f.host = strings.TrimPrefix(f.srv.URL, "https://")
	f.rt = &breakableRT{base: f.srv.Client().Transport}

	f.projects["acme/pay"] = &fProject{owned: true, head: "refs/heads/main"}     // Writable
	f.projects["acme/public"] = &fProject{head: "refs/heads/master"}             // ReadOnly
	f.projects["acme/site"] = &fProject{owned: true, head: "refs/heads/main"}    // Admin
	f.projects["acme/scratch"] = &fProject{owned: true, head: "refs/heads/main"} // Deletable
	f.projects["acme/team/widget"] = &fProject{owned: true, head: "refs/heads/main"}
	// A project whose name is not lowercase has no key; the listing skips it.
	f.projects["Acme/Shouty"] = &fProject{owned: true, head: "refs/heads/main"}
	// stranger/secret is not seeded: Gerrit answers a project the caller
	// cannot see with the 404 it gives one that does not exist.

	// acme/billing is answered AS acme/pay — the derivation fixture.
	f.renames = map[string]string{"acme/billing": "acme/pay"}
	return f
}

func (f *fixture) Host() string                 { return f.host }
func (f *fixture) Transport() http.RoundTripper { return f.rt }

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

// breakableRT records what the driver SENT, under the pin, then fails it when
// broken: an outage is not a status code.
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
	mux.HandleFunc("GET /a/accounts/self", f.self)
	mux.HandleFunc("GET /a/projects/{$}", f.list)
	mux.HandleFunc("GET /a/projects/{name}", f.project)
	mux.HandleFunc("GET /a/projects/{name}/HEAD", f.head)
	mux.HandleFunc("GET /a/projects/{name}/access", f.access)
	mux.HandleFunc("PUT /a/projects/{name}", f.create)
	mux.HandleFunc("POST /a/projects/{name}/access", f.restrict)
	mux.HandleFunc("DELETE /a/projects/{name}", f.delete)
	mux.HandleFunc("GET /a/groups/{group}", f.group)
	mux.HandleFunc("GET /a/groups/{group}/members/{account}", f.groupMember)

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

// fail answers the way Gerrit does: a status and a plain-text line that is
// never anything the caller sent.
func fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	w.WriteHeader(status)
	fmt.Fprintln(w, message)
}

func ok(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	fmt.Fprint(w, ")]}'\n")
	json.NewEncoder(w).Encode(body)
}

func basicOf(r *http.Request) string {
	raw, found := strings.CutPrefix(r.Header.Get("Authorization"), "Basic ")
	if !found {
		return ""
	}
	dec, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return ""
	}
	return string(dec)
}

// user gates every endpoint a CALLER's credential reaches.
func (f *fixture) user(w http.ResponseWriter, r *http.Request) bool {
	if basicOf(r) == goodToken {
		return true
	}
	fail(w, http.StatusUnauthorized, "Unauthorized")
	return false
}

func (f *fixture) self(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	ok(w, http.StatusOK, map[string]any{"_account_id": userID, "username": login, "email": login + "@example.invalid", "name": "Octo"})
}

// lookup resolves a requested name, following the one rename, and returns the
// name the project really has.
func (f *fixture) lookup(name string) (string, *fProject, bool) {
	if to, moved := f.renames[name]; moved {
		name = to
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, found := f.projects[name]
	return name, p, found
}

func (f *fixture) project(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	name, _, found := f.lookup(r.PathValue("name"))
	if !found {
		fail(w, http.StatusNotFound, "Not found: "+r.PathValue("name"))
		return
	}
	ok(w, http.StatusOK, map[string]any{"id": strings.ReplaceAll(name, "/", "%2F"), "name": name, "parent": "All-Projects", "state": "ACTIVE"})
}

func (f *fixture) head(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	_, p, found := f.lookup(r.PathValue("name"))
	if !found {
		fail(w, http.StatusNotFound, "Not found")
		return
	}
	ok(w, http.StatusOK, p.head)
}

func (f *fixture) access(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	_, p, found := f.lookup(r.PathValue("name"))
	if !found {
		fail(w, http.StatusNotFound, "Not found")
		return
	}
	// Gerrit OMITS is_owner and the local sections when the caller does not
	// own the project; it does not send false.
	body := map[string]any{"revision": "342dc1378cef126e0f6d90290b8814c49f2bbe80", "owner_of": []string{}, "groups": map[string]any{}}
	if p.owned {
		body["is_owner"] = true
		body["owner_of"] = []string{"refs/*"}
		local := map[string]any{"refs/*": map[string]any{"permissions": map[string]any{"owner": map[string]any{}}}}
		if p.restricted {
			local["refs/heads/*"] = map[string]any{"permissions": map[string]any{"read": map[string]any{"exclusive": true}}}
		}
		body["local"] = local
	}
	ok(w, http.StatusOK, body)
}

func (f *fixture) list(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	start, _ := strconv.Atoi(r.URL.Query().Get("S"))
	f.mu.Lock()
	names := make([]string, 0, len(f.projects))
	for name := range f.projects {
		names = append(names, name)
	}
	f.mu.Unlock()
	slices.Sort(names)
	out := map[string]any{}
	for i := start; i < len(names) && i < start+n; i++ {
		entry := map[string]any{"id": names[i], "state": "ACTIVE"}
		if i == start+n-1 && i < len(names)-1 {
			entry["_more_projects"] = true
		}
		out[names[i]] = entry
	}
	ok(w, http.StatusOK, out)
}

func (f *fixture) create(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	name := r.PathValue("name")
	type requestBody struct {
		Name   string   `json:"name"`
		Owners []string `json:"owners"`
	}
	var body requestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.Name != "" && body.Name != name) {
		fail(w, http.StatusBadRequest, "name must match URL")
		return
	}
	if strings.HasPrefix(name, "stranger/") {
		fail(w, http.StatusForbidden, "create project not permitted")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, taken := f.projects[name]; taken {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprintf(w, "Project %q already exists\n", name)
		return
	}
	owned := len(body.Owners) == 1 && body.Owners[0] == "user:"+login
	f.projects[name] = &fProject{owned: owned, head: "refs/heads/master"}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, ")]}'\n{\"id\":%q,\"name\":%q}\n", strings.ReplaceAll(name, "/", "%2F"), name)
}

func (f *fixture) restrict(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	type permissionBody struct {
		Exclusive bool `json:"exclusive"`
	}
	type accessBody struct {
		Permissions map[string]permissionBody `json:"permissions"`
	}
	type requestBody struct {
		Add map[string]accessBody `json:"add"`
	}
	var body requestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad request")
		return
	}
	_, p, found := f.lookup(r.PathValue("name"))
	switch {
	case !found:
		fail(w, http.StatusNotFound, "Not found")
	case !p.owned:
		fail(w, http.StatusForbidden, "not permitted: write ref config on refs/*")
	default:
		f.mu.Lock()
		p.restricted = body.Add["refs/heads/*"].Permissions["read"].Exclusive
		f.mu.Unlock()
		ok(w, http.StatusOK, map[string]any{"revision": "x"})
	}
}

func (f *fixture) delete(w http.ResponseWriter, r *http.Request) {
	if !f.user(w, r) {
		return
	}
	name := r.PathValue("name")
	f.mu.Lock()
	defer f.mu.Unlock()
	p, found := f.projects[name]
	switch {
	case !found:
		fail(w, http.StatusNotFound, "Not found: "+name)
	case !p.owned:
		fail(w, http.StatusForbidden, "not allowed to delete project")
	default:
		delete(f.projects, name)
		w.WriteHeader(http.StatusNoContent)
	}
}

// group answers the deployment's credential and 404s for anyone else, which
// is Gerrit's answer to a group the caller cannot see.
func (f *fixture) group(w http.ResponseWriter, r *http.Request) {
	g := r.PathValue("group")
	if basicOf(r) != memberToken || g != namespace {
		fail(w, http.StatusNotFound, g)
		return
	}
	ok(w, http.StatusOK, map[string]any{"name": namespace, "id": "877ea1f306aa74efc87e2ff2bc19c20f75e9e3ca"})
}

func (f *fixture) groupMember(w http.ResponseWriter, r *http.Request) {
	g, a := r.PathValue("group"), r.PathValue("account")
	switch {
	case basicOf(r) != memberToken || g != namespace:
		fail(w, http.StatusNotFound, g)
	case a != member:
		fail(w, http.StatusNotFound, "Not found: "+a)
	default:
		ok(w, http.StatusOK, map[string]any{"_account_id": 1000001, "username": member})
	}
}
