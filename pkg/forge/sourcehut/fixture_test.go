package sourcehut_test

// An httptest server answering the driver's GraphQL documents by operation
// name, in the shapes git.sr.ht's schema gives them; it does not execute
// GraphQL. No maintained container image of git.sr.ht exists, so this proves
// the driver against the schema, not against a server.

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
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
	goodToken = "srht-tok-6b1f0c9e2a7d"
	badToken  = "srht-tok-revoked-41ce88"

	me   = "tau"
	meID = 4711
)

type fRepo struct {
	id      int64
	owner   string // without the tilde
	name    string
	private bool
	access  string // the credential's grant: "RO", "RW", or "" for none
}

type fKey struct {
	repo    int64
	key     string
	comment string
	access  string
}

type fHook struct {
	repo  int64
	url   string
	query string
	wire  string
}

type fixture struct {
	t    *testing.T
	srv  *httptest.Server
	rt   *breakableRT
	host string
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey

	renames map[string]string

	mu             sync.Mutex
	repos          map[string]*fRepo // "owner/name"
	keys           map[string]*fKey  // rid
	hooks          map[int64]*fHook
	seq            int64
	lastWire       string
	lastRO         bool
	redirect       string
	redirectStatus int

	keyAccess   string // the mode createDeployKey reports, when set
	keyRepo     string // the repository createDeployKey reports, when set
	pageForever bool   // every listing answers a fresh cursor
	pageRepeat  bool   // every listing answers the same cursor
	gqlErrors   map[string]string
	nullData    map[string]bool
	raw         map[string]string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		t: t, pub: pub, priv: priv,
		repos: map[string]*fRepo{}, keys: map[string]*fKey{}, hooks: map[int64]*fHook{},
		seq: 100, gqlErrors: map[string]string{}, nullData: map[string]bool{}, raw: map[string]string{},
	}
	f.srv = httptest.NewTLSServer(f.handler())
	t.Cleanup(f.srv.Close)
	f.host = strings.TrimPrefix(f.srv.URL, "https://")
	f.rt = &breakableRT{base: f.srv.Client().Transport}

	// Writable is the credential's own: the listing omits ACL grants, which
	// forge.Lister allows, and the suite asks the listing for Writable. The ACL
	// grant, ~acme/pay, is asserted in driver_test.go.
	f.seed("tau", "pay", true, "RW")
	f.seed("acme", "pay", true, "RW")
	f.seed("acme", "public", false, "RO")  // ReadOnly
	f.seed("tau", "site", true, "RW")      // Admin: the credential's own
	f.seed("tau", "scratch", true, "RW")   // Deletable
	f.seed("stranger", "secret", true, "") // NoAccess: answered as null
	// ~tau/billing was renamed to ~tau/pay; this fixture still answers the
	// old name with the new repository.
	f.renames = map[string]string{"tau/billing": "tau/pay"}
	return f
}

func (f *fixture) seed(owner, name string, private bool, access string) *fRepo {
	f.seq++
	r := &fRepo{id: f.seq, owner: owner, name: name, private: private, access: access}
	f.repos[owner+"/"+name] = r
	return r
}

func (f *fixture) Host() string                 { return f.host }
func (f *fixture) Transport() http.RoundTripper { return f.rt }
func (f *fixture) PublicKey() string {
	return b64(f.pub)
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
	id, err := strconv.ParseInt(string(h), 10, 64)
	if err != nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.hooks[id]
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

type gqlRequest struct {
	Query         string         `json:"query"`
	OperationName string         `json:"operationName"`
	Variables     map[string]any `json:"variables"`
}

func (f *fixture) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		loc, status := f.redirect, f.redirectStatus
		f.redirect, f.redirectStatus = "", 0
		f.mu.Unlock()
		if loc != "" {
			http.Redirect(w, r, loc, status)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/query" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+goodToken {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"errors":[{"message":"Invalid or expired token"}]}`)
			return
		}
		var q gqlRequest
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if raw, ok := f.raw[q.OperationName]; ok {
			fmt.Fprint(w, raw)
			return
		}
		if msg, ok := f.gqlErrors[q.OperationName]; ok {
			json.NewEncoder(w).Encode(map[string]any{"data": nil, "errors": []map[string]string{{"message": msg}}})
			return
		}
		data, msg := f.answer(q)
		if f.nullData[q.OperationName] {
			data = nil
		}
		if msg != "" {
			json.NewEncoder(w).Encode(map[string]any{"data": nil, "errors": []map[string]string{{"message": msg}}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
}

func str(v map[string]any, k string) string {
	if s, ok := v[k].(string); ok {
		return s
	}
	return ""
}

func num(v map[string]any, k string) int64 {
	if n, ok := v[k].(float64); ok {
		return int64(n)
	}
	return 0
}

func obj(v map[string]any, k string) map[string]any {
	if m, ok := v[k].(map[string]any); ok {
		return m
	}
	return nil
}

func (f *fixture) repoJSON(r *fRepo) map[string]any {
	vis := "PUBLIC"
	if r.private {
		vis = "PRIVATE"
	}
	access := r.access
	if access == "" {
		access = "RO"
	}
	return map[string]any{
		"id": r.id, "rid": "rid-" + strconv.FormatInt(r.id, 10), "name": r.name, "visibility": vis, "access": access,
		"owner": map[string]any{"canonicalName": "~" + r.owner},
		"HEAD":  map[string]any{"name": "refs/heads/master"},
	}
}

// visible is the repository owner/name names, as this credential sees it.
func (f *fixture) visible(owner, name string) *fRepo {
	full := owner + "/" + name
	if to, ok := f.renames[full]; ok {
		full = to
	}
	r, ok := f.repos[full]
	if !ok || (r.private && r.access == "" && r.owner != me) {
		return nil
	}
	return r
}

func (f *fixture) byID(id int64) *fRepo {
	for _, r := range f.repos {
		if r.id == id {
			return r
		}
	}
	return nil
}

func (f *fixture) cursor() any {
	switch {
	case f.pageRepeat:
		return "same"
	case f.pageForever:
		f.seq++
		return "c" + strconv.FormatInt(f.seq, 10)
	}
	return nil
}

var aliasLine = regexp.MustCompile(`(?m)^\s*(tau_[0-9a-f]+): __typename\s*$`)

func (f *fixture) answer(q gqlRequest) (any, string) {
	v := q.Variables
	switch q.OperationName {
	case "Me":
		return map[string]any{"me": map[string]any{"id": meID, "username": me, "canonicalName": "~" + me, "email": "tau@example.invalid"}}, ""

	case "Repository", "Access":
		out := map[string]any{"me": map[string]any{"canonicalName": "~" + me}}
		user := map[string]any{"repository": nil}
		if r := f.visible(str(v, "owner"), str(v, "name")); r != nil {
			user["repository"] = f.repoJSON(r)
		}
		out["user"] = user
		return out, ""

	case "List":
		var results []any
		names := make([]string, 0, len(f.repos))
		for full := range f.repos {
			names = append(names, full)
		}
		sort.Strings(names)
		for _, full := range names {
			if r := f.repos[full]; r.owner == me {
				results = append(results, f.repoJSON(r))
			}
		}
		if v["cursor"] != nil && !f.pageForever && !f.pageRepeat {
			results = nil
		}
		return map[string]any{"me": map[string]any{"repositories": map[string]any{"results": results, "cursor": f.cursor()}}}, ""

	case "Create":
		full := me + "/" + str(v, "name")
		if _, ok := f.repos[full]; ok {
			return nil, "A repository with this name already exists."
		}
		r := f.seed(me, str(v, "name"), str(v, "visibility") == "PRIVATE", "RW")
		return map[string]any{"createRepository": f.repoJSON(r)}, ""

	case "Delete":
		r := f.byID(num(v, "id"))
		if r == nil || r.owner != me {
			return nil, "Access denied"
		}
		delete(f.repos, r.owner+"/"+r.name)
		return map[string]any{"deleteRepository": f.repoJSON(r)}, ""

	case "InstallKey":
		var r *fRepo
		for _, x := range f.repos {
			if "rid-"+strconv.FormatInt(x.id, 10) == str(v, "repo") {
				r = x
			}
		}
		if r == nil || r.owner != me {
			return nil, "Access denied"
		}
		f.seq++
		rid := "key-" + strconv.FormatInt(f.seq, 10)
		parts := strings.SplitN(str(v, "key"), " ", 3)
		k := &fKey{repo: r.id, key: parts[0] + " " + parts[1], comment: parts[2], access: "RO"}
		if f.keyAccess != "" {
			k.access = f.keyAccess
		}
		f.keys[rid] = k
		f.lastRO = k.access == "RO"
		on := map[string]any{"name": r.name, "owner": map[string]any{"canonicalName": "~" + r.owner}}
		if f.keyRepo != "" {
			on["name"] = f.keyRepo
		}
		return map[string]any{"createDeployKey": map[string]any{"rid": rid, "access": k.access, "repo": on}}, ""

	case "Keys":
		r := f.visible(str(v, "owner"), str(v, "name"))
		if r == nil {
			return map[string]any{"user": map[string]any{"repository": nil}}, ""
		}
		var results []any
		rids := make([]string, 0, len(f.keys))
		for rid := range f.keys {
			rids = append(rids, rid)
		}
		sort.Strings(rids)
		for _, rid := range rids {
			if k := f.keys[rid]; k.repo == r.id {
				results = append(results, map[string]any{"rid": rid, "key": k.key, "comment": k.comment})
			}
		}
		if v["cursor"] != nil && !f.pageForever && !f.pageRepeat {
			results = nil
		}
		repo := map[string]any{"name": r.name, "owner": map[string]any{"canonicalName": "~" + r.owner},
			"deployKeys": map[string]any{"results": results, "cursor": f.cursor()}}
		return map[string]any{"user": map[string]any{"repository": repo}}, ""

	case "RemoveKey":
		rid := str(v, "rid")
		if _, ok := f.keys[rid]; !ok {
			return map[string]any{"deleteDeployKey": nil}, ""
		}
		delete(f.keys, rid)
		return map[string]any{"deleteDeployKey": map[string]any{"rid": rid}}, ""

	case "RegisterHook":
		cfg := obj(v, "config")
		r := f.byID(num(cfg, "repositoryID"))
		if r == nil || r.owner != me {
			return nil, "Access denied"
		}
		m := aliasLine.FindStringSubmatch(str(cfg, "query"))
		if m == nil {
			return nil, "fixture: the query echoes no binding"
		}
		f.seq++
		f.hooks[f.seq] = &fHook{repo: r.id, url: str(cfg, "url"), query: str(cfg, "query"), wire: m[1]}
		f.lastWire = m[1]
		return map[string]any{"createGitWebhook": map[string]any{"id": f.seq, "url": str(cfg, "url")}}, ""

	case "Hooks":
		id := num(v, "id")
		var results []any
		ids := make([]int64, 0, len(f.hooks))
		for hid := range f.hooks {
			ids = append(ids, hid)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, hid := range ids {
			if h := f.hooks[hid]; h.repo == id {
				results = append(results, map[string]any{"id": hid, "url": h.url})
			}
		}
		if v["cursor"] != nil && !f.pageForever && !f.pageRepeat {
			results = nil
		}
		return map[string]any{"gitWebhooks": map[string]any{"results": results, "cursor": f.cursor()}}, ""

	case "RemoveHook":
		id := num(v, "id")
		if _, ok := f.hooks[id]; !ok {
			return nil, "No such webhook"
		}
		delete(f.hooks, id)
		return map[string]any{"deleteGitWebhook": map[string]any{"id": id}}, ""
	}
	return nil, "fixture: unknown operation " + q.OperationName
}
