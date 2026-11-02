package azure_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/taubyte/tau/pkg/forge/forgetest"
	"github.com/taubyte/tau/pkg/git/repokey"
)

const (
	goodToken = "az-pat-3k9q2m7x4v1c8b"
	badToken  = "az-pat-revoked-6h2j5n"
	// sickToken is answered the way Azure DevOps answers a credential it does
	// not accept on many endpoints: 203 and an HTML sign-in page.
	sickToken = "az-pat-signin-1f8e2d"

	org       = "acme"
	userID    = "6f1a9a3e-2b7c-4d1e-9f00-3c5b7a2e1d44"
	account   = "octo@example.invalid"
	gitNS     = "2e9eb7ed-3c0a-47d4-87c1-0ffdd275fd87"
	permRead  = 2
	permPush  = 4
	permAdmin = 8192
)

type fRepo struct {
	id, project, name string
	bits              int
	disabled          bool
}

type fProject struct {
	id, name, visibility string
}

type fixture struct {
	srv  *httptest.Server
	rt   *breakableRT
	host string

	renames map[string]string

	mu             sync.Mutex
	projects       map[string]*fProject
	repos          map[string]*fRepo // "project/name"
	seq            int
	redirect       string
	redirectStatus int

	evalDrop     bool // answer the permission batch with one evaluation missing
	evalForeign  bool // answer it about another token
	listExtra    []map[string]any
	answerAs     map[string]any // the next repository GET answers with this body
	createAnswer map[string]any
	emptyCreate  bool
	noAccount    bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		projects: map[string]*fProject{},
		repos:    map[string]*fRepo{},
	}
	f.srv = httptest.NewTLSServer(f.handler())
	t.Cleanup(f.srv.Close)
	f.host = strings.TrimPrefix(f.srv.URL, "https://")
	f.rt = &breakableRT{base: f.srv.Client().Transport}

	for _, p := range []fProject{
		{"p-pay", "pay", "private"}, {"p-public", "public", "public"}, {"p-site", "site", "private"},
		{"p-scratch", "scratch", "private"}, {"p-locked", "locked", "private"},
	} {
		f.projects[p.name] = &p
	}
	f.seed("pay", "pay", permRead|permPush)                    // Writable
	f.seed("public", "public", permRead)                       // ReadOnly
	f.seed("site", "site", permRead|permPush|permAdmin)        // Admin
	f.seed("scratch", "scratch", permRead|permPush|permAdmin)  // Deletable
	f.seed("pay", "frozen", permRead|permPush).disabled = true // never listed
	// NoAccess (acme/secret/_git/secret) and Missing are not seeded: Azure
	// DevOps answers both with the same 404.
	f.renames = map[string]string{"pay/billing": "pay/pay"}
	return f
}

func (f *fixture) seed(project, name string, bits int) *fRepo {
	f.seq++
	r := &fRepo{id: fmt.Sprintf("r-%04d", f.seq), project: project, name: name, bits: bits}
	f.repos[project+"/"+name] = r
	return r
}

func (f *fixture) Host() string                 { return f.host }
func (f *fixture) Transport() http.RoundTripper { return f.rt }

func (f *fixture) key(t *testing.T, path string) repokey.Key {
	t.Helper()
	k, err := repokey.Parse("https://" + f.host + "/" + path)
	if err != nil {
		t.Fatalf("fixture: %q is not a key: %v", path, err)
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

type breakableRT struct {
	base   http.RoundTripper
	broken atomic.Bool
	mu     sync.Mutex
	sent   []forgetest.Request
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
	mux.HandleFunc("GET /{org}/_apis/connectionData", f.connectionData)
	mux.HandleFunc("GET /{org}/_apis/git/repositories", f.listRepos)
	mux.HandleFunc("GET /{org}/{project}/_apis/git/repositories/{repo}", f.getRepo)
	mux.HandleFunc("GET /{org}/_apis/projects/{project}", f.getProject)
	mux.HandleFunc("POST /{org}/{project}/_apis/git/repositories", f.createRepo)
	mux.HandleFunc("DELETE /{org}/{project}/_apis/git/repositories/{id}", f.deleteRepo)
	mux.HandleFunc("POST /{org}/_apis/security/permissionevaluationbatch", f.evaluate)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		loc, status := f.redirect, f.redirectStatus
		f.redirect, f.redirectStatus = "", 0
		f.mu.Unlock()
		if loc != "" {
			http.Redirect(w, r, loc, status)
			return
		}
		if r.URL.Query().Get("api-version") == "" {
			f.fail(w, http.StatusBadRequest, "VssVersionNotSpecifiedException")
			return
		}
		if first, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/"); first != org {
			f.fail(w, http.StatusNotFound, "TF400898: organization not found")
			return
		}
		switch pat(r) {
		case goodToken:
		case sickToken:
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNonAuthoritativeInfo)
			fmt.Fprint(w, "<html>Sign in</html>")
			return
		default:
			f.fail(w, http.StatusUnauthorized, "TF400813: The user is not authorized to access this resource.")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// pat is the token from "Basic base64(:pat)", the PAT form.
func pat(r *http.Request) string {
	enc, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Basic ")
	if !ok {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return ""
	}
	_, tok, _ := strings.Cut(string(raw), ":")
	return tok
}

func (f *fixture) fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"message": message, "typeKey": "VssServiceException"})
}

func (f *fixture) ok(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func (f *fixture) connectionData(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	none := f.noAccount
	f.mu.Unlock()
	if none {
		f.ok(w, http.StatusOK, map[string]any{"authenticatedUser": map[string]any{"id": userID}})
		return
	}
	f.ok(w, http.StatusOK, map[string]any{
		"authenticatedUser": map[string]any{
			"id":                  userID,
			"providerDisplayName": "Octo Tau",
			"properties": map[string]any{
				"Account": map[string]any{"$type": "System.String", "$value": account},
			},
		},
		"instanceId": "00000000-0000-0000-0000-000000000001",
	})
}

func (f *fixture) repoBody(r *fRepo) map[string]any {
	p := f.projects[r.project]
	path := org + "/" + r.project + "/_git/" + r.name
	return map[string]any{
		"id":   r.id,
		"name": r.name,
		"url":  "https://" + f.host + "/" + org + "/" + p.id + "/_apis/git/repositories/" + r.id,
		"project": map[string]any{
			"id": p.id, "name": p.name, "state": "wellFormed", "visibility": p.visibility,
		},
		"defaultBranch": "refs/heads/main",
		"size":          1024,
		"remoteUrl":     "https://" + org + "@" + f.host + "/" + path,
		"sshUrl":        "ssh://git@" + f.host + "/" + path,
		"webUrl":        "https://" + f.host + "/" + path,
		"isDisabled":    r.disabled,
	}
}

func (f *fixture) lookup(project, name string) (*fRepo, bool) {
	full := project + "/" + name
	if to, moved := f.renames[full]; moved {
		full = to
	}
	r, ok := f.repos[full]
	if !ok {
		for _, c := range f.repos {
			if c.project == project && c.id == name {
				return c, true
			}
		}
	}
	return r, ok
}

func (f *fixture) getRepo(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	if f.answerAs != nil {
		body := f.answerAs
		f.answerAs = nil
		f.mu.Unlock()
		f.ok(w, http.StatusOK, body)
		return
	}
	repo, ok := f.lookup(r.PathValue("project"), r.PathValue("repo"))
	var body map[string]any
	if ok {
		body = f.repoBody(repo)
	}
	f.mu.Unlock()
	if !ok {
		f.fail(w, http.StatusNotFound, "TF401019: The Git repository with name or identifier does not exist or you do not have permissions for the operation you are attempting.")
		return
	}
	f.ok(w, http.StatusOK, body)
}

func (f *fixture) listRepos(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	keys := make([]string, 0, len(f.repos))
	for k := range f.repos {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := []map[string]any{}
	out = append(out, f.listExtra...)
	for _, k := range keys {
		out = append(out, f.repoBody(f.repos[k]))
	}
	f.mu.Unlock()
	f.ok(w, http.StatusOK, map[string]any{"value": out, "count": len(out)})
}

func (f *fixture) getProject(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	p, ok := f.projects[r.PathValue("project")]
	f.mu.Unlock()
	if !ok {
		f.fail(w, http.StatusNotFound, "TF200016: The following project does not exist")
		return
	}
	f.ok(w, http.StatusOK, map[string]any{"id": p.id, "name": p.name, "visibility": p.visibility, "state": "wellFormed"})
}

func (f *fixture) createRepo(w http.ResponseWriter, r *http.Request) {
	type projectRef struct {
		ID string `json:"id"`
	}
	type requestBody struct {
		Name    string     `json:"name"`
		Project projectRef `json:"project"`
	}
	var body requestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		f.fail(w, http.StatusBadRequest, "invalid request")
		return
	}
	project := r.PathValue("project")
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[project]
	switch {
	case !ok || p.id != body.Project.ID:
		f.fail(w, http.StatusNotFound, "TF200016: project does not exist")
		return
	case project == "locked":
		f.fail(w, http.StatusForbidden, "TF401027: You need the Git 'CreateRepository' permission to perform this action.")
		return
	}
	if _, taken := f.repos[project+"/"+body.Name]; taken {
		f.fail(w, http.StatusConflict, "TF400948: A Git repository with the name already exists.")
		return
	}
	if f.emptyCreate {
		f.ok(w, http.StatusCreated, map[string]any{})
		return
	}
	if f.createAnswer != nil {
		f.ok(w, http.StatusCreated, f.createAnswer)
		return
	}
	created := f.seed(project, body.Name, permRead|permPush|permAdmin)
	f.ok(w, http.StatusCreated, f.repoBody(created))
}

func (f *fixture) deleteRepo(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for full, repo := range f.repos {
		if repo.project == r.PathValue("project") && repo.id == r.PathValue("id") {
			if repo.bits&permAdmin == 0 {
				f.fail(w, http.StatusForbidden, "TF401027: You need the Git 'DeleteRepository' permission.")
				return
			}
			delete(f.repos, full)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	f.fail(w, http.StatusNotFound, "TF401019")
}

func (f *fixture) evaluate(w http.ResponseWriter, r *http.Request) {
	type evaluation struct {
		SecurityNamespaceID string `json:"securityNamespaceId"`
		Token               string `json:"token"`
		Permissions         int    `json:"permissions"`
	}
	type requestBody struct {
		Evaluations []evaluation `json:"evaluations"`
	}
	var body requestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.fail(w, http.StatusBadRequest, "invalid request")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []map[string]any{}
	for i, e := range body.Evaluations {
		if f.evalDrop && i == 0 {
			continue
		}
		held := false
		for _, repo := range f.repos {
			if e.SecurityNamespaceID == gitNS && e.Token == "repoV2/"+f.projects[repo.project].id+"/"+repo.id {
				held = repo.bits&e.Permissions != 0
			}
		}
		tok := e.Token
		if f.evalForeign {
			tok = "repoV2/elsewhere/other"
		}
		out = append(out, map[string]any{
			"securityNamespaceId": e.SecurityNamespaceID, "token": tok, "permissions": e.Permissions, "value": held,
		})
	}
	f.ok(w, http.StatusOK, map[string]any{"alwaysAllowAdministrators": false, "evaluations": out})
}

func (f *fixture) adminOf(repoID string) bool {
	for _, r := range f.repos {
		if r.id == repoID {
			return r.bits&permAdmin != 0
		}
	}
	return false
}
