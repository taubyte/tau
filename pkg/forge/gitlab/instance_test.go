package gitlab

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/v2/forge"
	"github.com/taubyte/tau/pkg/v2/forge/forgetest"
)

// instance is a GitLab, in one process, over httptest. NOTHING HERE REACHES THE
// NETWORK: a test that talks to gitlab.com is not a test, it is a report on
// somebody else's uptime.
//
// It answers the endpoints this driver actually calls, in the shapes GitLab
// documents — a project addressed by its URL-encoded full path, a namespace id
// that has to be resolved before a project can be created, a members endpoint
// keyed by a numeric user id, a GraphQL endpoint that answers 200 whatever
// happens — because the whole point of a second driver is to find where the
// interface was shaped like the first forge, and a fixture that answers in
// GitHub's shapes would find nothing.
type instance struct {
	srv  *httptest.Server
	rec  *recorder
	host string
	base string

	// moved are the projects this instance still answers for under a path they
	// no longer have. It is written before the server is handed out and never
	// again, so it is read without the lock.
	moved map[string]string

	mu             sync.Mutex
	projects       map[string]*proj
	namespaces     map[string]int64
	hooks          map[int64]*hookRecord
	deployKeys     map[int64]*deployKeyRecord
	users          map[string]*userRecord
	members        map[string]int
	seq            int64
	lastWire       string
	lastCanPush    bool
	redirect       string
	redirectStatus int
}

type proj struct {
	path          string
	visibility    string // "public" or "private"
	grant         forge.Access
	defaultBranch string
}

type hookRecord struct {
	project string
	url     string
	wire    string
}

type deployKeyRecord struct {
	project string
	title   string
	canPush bool
}

type userRecord struct {
	id    int64
	state string
}

// The credentials this instance honours. They are long and odd so that finding
// one where it must not be is unambiguous.
const (
	goodToken   = "glpat-conformance-6f1c8a2e"
	badToken    = "glpat-revoked-b0b0b0b0"
	deployToken = "glpat-deployment-4d4d4d4d"
)

// Membership fixtures. alice is in acme; mallory exists and is not.
const (
	fixtureGroup     = "acme"
	fixtureMember    = "alice"
	fixtureNonMember = "mallory"
)

var errUnreachable = errors.New("gitlab-test: the instance is unreachable")

// recorder is the transport PinnedTransport wraps. It records what would go on
// the wire — AFTER the credential was attached, which is the only place worth
// looking for it — and it is where Break makes a call fail, so an outage is a
// transport failure and not a status code pretending to be one.
type recorder struct {
	base http.RoundTripper

	mu     sync.Mutex
	reqs   []forgetest.Request
	broken bool
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, forgetest.Request{
		Method: req.Method,
		URL:    req.URL.String(),
		Header: req.Header.Clone(),
	})
	broken := r.broken
	r.mu.Unlock()
	if broken {
		return nil, errUnreachable
	}
	return r.base.RoundTrip(req)
}

func (r *recorder) drain() []forgetest.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.reqs
	r.reqs = nil
	return out
}

func (r *recorder) setBroken(b bool) {
	r.mu.Lock()
	r.broken = b
	r.mu.Unlock()
}

// keys are the conformance fixtures on this instance, named by what the suite
// asks of them.
type keys struct {
	Writable    repokey.Key
	ReadOnly    repokey.Key
	NoAccess    repokey.Key
	Missing     repokey.Key
	Admin       repokey.Key
	Deletable   repokey.Key
	Creatable   repokey.Key
	Uncreatable repokey.Key
	Deep        repokey.Key
	Moved       repokey.Key
	MovedTo     repokey.Key
}

func newInstance(t *testing.T) *instance {
	t.Helper()
	in := &instance{
		projects:   make(map[string]*proj),
		namespaces: map[string]int64{"acme": 42, "acme/team": 43},
		hooks:      make(map[int64]*hookRecord),
		deployKeys: make(map[int64]*deployKeyRecord),
		users: map[string]*userRecord{
			fixtureMember:    {id: 7, state: "active"},
			fixtureNonMember: {id: 11, state: "active"},
			"blocked":        {id: 13, state: "blocked"},
		},
		// access levels: 30 is Developer.
		members: map[string]int{fixtureGroup + "/7": 30},
	}
	in.srv = httptest.NewServer(http.HandlerFunc(in.serve))
	t.Cleanup(in.srv.Close)

	in.base = in.srv.URL
	in.host = strings.TrimPrefix(in.srv.URL, "http://")
	in.rec = &recorder{base: http.DefaultTransport}

	// acme/team/widget is seeded on every instance, so that a driver refusing a
	// nested key would be refusing a project that genuinely exists.
	in.seed("acme/pay", "private", forge.AccessWrite)
	in.seed("acme/public", "public", forge.AccessNone)
	in.seed("stranger/secret", "private", forge.AccessNone)
	in.seed("acme/site", "private", forge.AccessAdmin)
	in.seed("acme/scratch", "private", forge.AccessAdmin)
	in.seed("acme/team/widget", "private", forge.AccessAdmin)

	// acme/billing was renamed to acme/pay and this instance still answers for
	// the old path, WITH the project it became — whose http_url_to_repo says
	// which one that is. It is the only fixture here on which a driver echoing
	// back the key it was asked about answers differently from one deriving the
	// key from what the instance said, which is what the conformance suite
	// asserts on it. Serving it in one hop rather than 301-ing to it keeps that
	// assertion about derivation instead of about redirect policy.
	in.moved = map[string]string{"acme/billing": "acme/pay"}
	return in
}

func (in *instance) seed(path, visibility string, grant forge.Access) {
	in.projects[path] = &proj{path: path, visibility: visibility, grant: grant, defaultBranch: "main"}
}

func (in *instance) key(t *testing.T, full string) repokey.Key {
	t.Helper()
	k, err := repokey.Parse(in.base + "/" + full + ".git")
	if err != nil {
		t.Fatalf("fixture %q: %v", full, err)
	}
	return k
}

func (in *instance) keys(t *testing.T) keys {
	t.Helper()
	return keys{
		Writable:    in.key(t, "acme/pay"),
		ReadOnly:    in.key(t, "acme/public"),
		NoAccess:    in.key(t, "stranger/secret"),
		Missing:     in.key(t, "acme/nope"),
		Admin:       in.key(t, "acme/site"),
		Deletable:   in.key(t, "acme/scratch"),
		Creatable:   in.key(t, "acme/fresh"),
		Uncreatable: in.key(t, "stranger/fresh"),
		Deep:        in.key(t, "acme/team/widget"),
		Moved:       in.key(t, "acme/billing"),
		MovedTo:     in.key(t, "acme/pay"),
	}
}

// redirectOnce makes the next request this instance answers with status,
// pointing at loc. A fixture records what ARRIVED at it, so where a request went
// instead is the one thing it cannot report; this is what lets the suite go and
// look at the other end.
//
// The status is the suite's and not this instance's: 301, 302 and 303 make Go
// rewrite a write to a GET and 307 and 308 replay it, so the two reach
// different halves of the defect, and only the caller knows which half it is
// asking about.
func (in *instance) redirectOnce(loc string, status int) {
	in.mu.Lock()
	in.redirect, in.redirectStatus = loc, status
	in.mu.Unlock()
}

// driver builds the driver under test, pointed at this instance. The host is the
// loopback authority the server bound to, which is the whole self-hosted case:
// nothing in the driver knows the string "gitlab.com".
func (in *instance) driver(t *testing.T, opts ...Option) forge.Driver {
	t.Helper()
	opts = append([]Option{BaseURL(in.base), HTTPTransport(in.rec)}, opts...)
	d, err := New(in.host, opts...)
	if err != nil {
		t.Fatalf("building a driver for %q: %v", in.host, err)
	}
	return d
}

// ─────────────────────────────── the handler ───────────────────────────────

func (in *instance) serve(w http.ResponseWriter, r *http.Request) {
	in.mu.Lock()
	loc, status := in.redirect, in.redirectStatus
	in.redirect, in.redirectStatus = "", 0
	in.mu.Unlock()
	if loc != "" {
		// An instance answering a 3xx to somewhere else. Every SDK runs on an
		// http.Client and an http.Client follows redirects, so what stops the
		// credential going with it is forge.PinnedTransport and nothing in this
		// driver's own code.
		http.Redirect(w, r, loc, status)
		return
	}

	token, presented := credential(r)
	if presented && token != goodToken && token != deployToken {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_token"})
		return
	}

	// EscapedPath and not Path: a project is addressed by its full path with the
	// separators percent-encoded, so decoding first would make acme%2Fpay
	// indistinguishable from a route with a second segment.
	path := r.URL.EscapedPath()
	rest, isProject := strings.CutPrefix(path, "/api/v4/projects/")
	group, isGroup := strings.CutPrefix(path, "/api/v4/groups/")
	namespace, isNamespace := strings.CutPrefix(path, "/api/v4/namespaces/")

	switch {
	case path == "/api/v4/user":
		in.serveUser(w, token, presented)
	case path == "/api/graphql":
		in.serveGraphQL(w, r, token)
	case path == "/api/v4/users":
		in.serveUsers(w, r, presented)
	case path == "/api/v4/projects" && r.Method == http.MethodGet:
		in.serveList(w, r, token)
	case path == "/api/v4/projects" && r.Method == http.MethodPost:
		in.serveCreate(w, r, token)
	case isProject:
		in.serveProject(w, r, token, rest)
	case isNamespace:
		in.serveNamespace(w, namespace, token)
	case isGroup:
		in.serveGroupMember(w, group, token)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "404 Not Found"})
	}
}

// credential reads GitLab's two credential headers. Both are real and they carry
// different credential kinds, which is the whole reason the driver has an option
// to pick one.
func credential(r *http.Request) (string, bool) {
	if t := r.Header.Get("PRIVATE-TOKEN"); t != "" {
		return t, true
	}
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && bearer != "" {
		return bearer, true
	}
	return "", false
}

func (in *instance) serveUser(w http.ResponseWriter, token string, presented bool) {
	if !presented {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "401 Unauthorized"})
		return
	}
	if token == deployToken {
		writeJSON(w, http.StatusOK, map[string]any{"id": 9, "username": "tau-deployment", "email": ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": 7, "username": "alice", "email": "alice@example.invalid"})
}

func (in *instance) serveGraphQL(w http.ResponseWriter, r *http.Request, token string) {
	var query struct {
		Variables struct {
			FullPath string `json:"fullPath"`
		} `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"errors": []any{map[string]any{"message": "unparseable query"}}})
		return
	}

	in.mu.Lock()
	defer in.mu.Unlock()
	p, ok := in.projects[query.Variables.FullPath]
	if !ok {
		// GitLab hides what it will not show, so a project that is not there and
		// one this credential may not see are one answer.
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"project": nil}})
		return
	}
	level, visible := p.access(token)
	if !visible {
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"project": nil}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"project": map[string]any{
		"fullPath": p.path,
		"userPermissions": map[string]any{
			"pushCode":     level.CanWrite(),
			"adminProject": level.CanAdmin(),
		},
	}}})
}

func (in *instance) serveUsers(w http.ResponseWriter, r *http.Request, presented bool) {
	if !presented {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "401 Unauthorized"})
		return
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	username := r.URL.Query().Get("username")
	u, ok := in.users[username]
	if !ok {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, []any{map[string]any{"id": u.id, "username": username, "state": u.state}})
}

func (in *instance) serveList(w http.ResponseWriter, r *http.Request, token string) {
	in.mu.Lock()
	defer in.mu.Unlock()

	paths := make([]string, 0, len(in.projects))
	for path, p := range in.projects {
		// membership=true is the projects this credential holds a grant on, and
		// it omits the public ones it merely reads.
		if _, visible := p.access(token); visible && p.grant != forge.AccessNone {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)

	// Two per page, so the driver's pagination loop is exercised rather than
	// merely present.
	const perPage = 2
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	start := (page - 1) * perPage
	if start > len(paths) {
		start = len(paths)
	}
	end := start + perPage
	if end > len(paths) {
		end = len(paths)
	}
	out := make([]any, 0, end-start)
	for _, path := range paths[start:end] {
		out = append(out, in.projectJSON(in.projects[path]))
	}
	if end < len(paths) {
		w.Header().Set("X-Next-Page", strconv.Itoa(page+1))
	}
	writeJSON(w, http.StatusOK, out)
}

func (in *instance) serveCreate(w http.ResponseWriter, r *http.Request, token string) {
	var body struct {
		Path        string `json:"path"`
		NamespaceID int64  `json:"namespace_id"`
		Visibility  string `json:"visibility"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "400 Bad request"})
		return
	}

	in.mu.Lock()
	defer in.mu.Unlock()
	if token != goodToken {
		writeJSON(w, http.StatusForbidden, map[string]any{"message": "403 Forbidden"})
		return
	}
	full := ""
	for path, id := range in.namespaces {
		if id == body.NamespaceID {
			full = path + "/" + body.Path
		}
	}
	if full == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "404 Namespace Not Found"})
		return
	}
	if _, taken := in.projects[full]; taken {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": map[string]any{"path": []string{"has already been taken"}}})
		return
	}
	visibility := "private"
	if body.Visibility == "public" {
		visibility = "public"
	}
	// An empty project has no branch yet, and GitLab still reports one: the
	// group or instance preference, which is the branch a first push creates.
	in.projects[full] = &proj{path: full, visibility: visibility, grant: forge.AccessAdmin, defaultBranch: "main"}
	writeJSON(w, http.StatusCreated, in.projectJSON(in.projects[full]))
}

// serveProject routes everything under /api/v4/projects/{encoded full path}.
func (in *instance) serveProject(w http.ResponseWriter, r *http.Request, token, rest string) {
	segments := strings.Split(rest, "/")
	full, err := url.PathUnescape(segments[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "400 Bad request"})
		return
	}

	if to, moved := in.moved[full]; moved {
		full = to
	}

	in.mu.Lock()
	defer in.mu.Unlock()
	p, ok := in.projects[full]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "404 Project Not Found"})
		return
	}
	level, visible := p.access(token)
	if !visible {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "404 Project Not Found"})
		return
	}

	switch {
	case len(segments) == 1 && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, in.projectJSON(p))
	case len(segments) == 1 && r.Method == http.MethodDelete:
		if !level.CanAdmin() {
			writeJSON(w, http.StatusForbidden, map[string]any{"message": "403 Forbidden"})
			return
		}
		delete(in.projects, full)
		// GitLab ACCEPTS a deletion; it does not perform one synchronously.
		writeJSON(w, http.StatusAccepted, map[string]any{"message": "202 Accepted"})
	case len(segments) == 2 && segments[1] == "hooks" && r.Method == http.MethodPost:
		in.createHook(w, r, full, level)
	case len(segments) == 3 && segments[1] == "hooks" && r.Method == http.MethodDelete:
		in.deleteResource(w, segments[2], level, func(id int64) bool {
			if h, ok := in.hooks[id]; ok && h.project == full {
				delete(in.hooks, id)
				return true
			}
			return false
		})
	case len(segments) == 2 && segments[1] == "deploy_keys" && r.Method == http.MethodPost:
		in.createDeployKey(w, r, full, level)
	case len(segments) == 3 && segments[1] == "deploy_keys" && r.Method == http.MethodDelete:
		in.deleteResource(w, segments[2], level, func(id int64) bool {
			if k, ok := in.deployKeys[id]; ok && k.project == full {
				delete(in.deployKeys, id)
				return true
			}
			return false
		})
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "404 Not Found"})
	}
}

func (in *instance) createHook(w http.ResponseWriter, r *http.Request, full string, level forge.Access) {
	if !level.CanAdmin() {
		// GitLab needs Maintainer for a webhook, which is strictly more than the
		// rung a push needs.
		writeJSON(w, http.StatusForbidden, map[string]any{"message": "403 Forbidden"})
		return
	}
	var body struct {
		URL          string `json:"url"`
		PushEvents   bool   `json:"push_events"`
		Token        string `json:"token"`
		SigningToken string `json:"signing_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "400 Bad request"})
		return
	}
	in.lastWire = body.Token
	if body.SigningToken != "" {
		in.lastWire = body.SigningToken
	}
	in.seq++
	id := 5000 + in.seq
	in.hooks[id] = &hookRecord{project: full, url: body.URL, wire: in.lastWire}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "url": body.URL, "push_events": body.PushEvents})
}

func (in *instance) createDeployKey(w http.ResponseWriter, r *http.Request, full string, level forge.Access) {
	if !level.CanAdmin() {
		writeJSON(w, http.StatusForbidden, map[string]any{"message": "403 Forbidden"})
		return
	}
	var body struct {
		Title   string `json:"title"`
		Key     string `json:"key"`
		CanPush bool   `json:"can_push"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "400 Bad request"})
		return
	}
	in.lastCanPush = body.CanPush
	in.seq++
	id := 6000 + in.seq
	in.deployKeys[id] = &deployKeyRecord{project: full, title: body.Title, canPush: body.CanPush}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "title": body.Title, "key": body.Key, "can_push": body.CanPush})
}

// deleteResource is the shared shape of removing a hook or a deploy key: an id
// that is not a number, or names nothing here, is a 404 and never a success.
func (in *instance) deleteResource(w http.ResponseWriter, raw string, level forge.Access, remove func(int64) bool) {
	if !level.CanAdmin() {
		writeJSON(w, http.StatusForbidden, map[string]any{"message": "403 Forbidden"})
		return
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "404 Not Found"})
		return
	}
	id, err := strconv.ParseInt(decoded, 10, 64)
	if err != nil || !remove(id) {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "404 Not Found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (in *instance) serveNamespace(w http.ResponseWriter, raw, token string) {
	full, err := url.PathUnescape(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "400 Bad request"})
		return
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	id, ok := in.namespaces[full]
	if !ok || token != goodToken {
		// A namespace this credential is not in is a namespace it cannot see.
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "404 Namespace Not Found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "full_path": full, "path": full})
}

// serveGroupMember answers /api/v4/groups/{group}/members/all/{user id}.
func (in *instance) serveGroupMember(w http.ResponseWriter, rest, token string) {
	segments := strings.Split(rest, "/")
	if len(segments) != 4 || segments[1] != "members" || segments[2] != "all" {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "404 Not Found"})
		return
	}
	if token != deployToken {
		// Only the deployment's credential may ask. A caller's own token gets a
		// 404 here, which is exactly the ambiguity the face exists to avoid.
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "404 Group Not Found"})
		return
	}
	group, err := url.PathUnescape(segments[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "400 Bad request"})
		return
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	level, ok := in.members[group+"/"+segments[3]]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "404 Not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": segments[3], "access_level": level, "state": "active"})
}

// access is what a token holds on a project, and whether it can see it at all. A
// public project is READABLE by everyone and that is all it is.
func (p *proj) access(token string) (forge.Access, bool) {
	if token == goodToken && p.grant != forge.AccessNone {
		return p.grant, true
	}
	if p.visibility == "public" {
		return forge.AccessRead, true
	}
	return forge.AccessNone, false
}

func (in *instance) projectJSON(p *proj) map[string]any {
	return map[string]any{
		"path_with_namespace": p.path,
		"http_url_to_repo":    in.base + "/" + p.path + ".git",
		"ssh_url_to_repo":     "ssh://git@" + in.host + "/" + p.path + ".git",
		"default_branch":      p.defaultBranch,
		"visibility":          p.visibility,
		// Present in the real answer and deliberately unread by the driver.
		"id": 999,
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// ─────────────────────────────── deliveries ───────────────────────────────

// deliveryPayload is GitLab's push event, in GitLab's shape. It carries the
// fields the driver reads and a couple it deliberately does not, so that
// "reading this little" is a property the fixture can actually test.
type deliveryPayload struct {
	ObjectKind   string  `json:"object_kind"`
	EventName    string  `json:"event_name"`
	Before       string  `json:"before"`
	After        string  `json:"after"`
	Ref          string  `json:"ref"`
	CheckoutSHA  *string `json:"checkout_sha"`
	UserUsername string  `json:"user_username"`
	Project      struct {
		Name              string `json:"name"`
		PathWithNamespace string `json:"path_with_namespace"`
		GitHTTPURL        string `json:"git_http_url"`
		GitSSHURL         string `json:"git_ssh_url"`
		DefaultBranch     string `json:"default_branch"`
	} `json:"project"`
	TotalCommitsCount int `json:"total_commits_count"`
}

func (in *instance) Delivery(h forge.Hook, ref, commit string) (forge.Delivery, error) {
	return in.deliver(h, h.Key, ref, commit, time.Now(), true)
}

func (in *instance) DeliveryFor(h forge.Hook, other repokey.Key, ref, commit string) (forge.Delivery, error) {
	return in.deliver(h, other, ref, commit, time.Now(), true)
}

func (in *instance) NonPushDelivery(h forge.Hook) (forge.Delivery, error) {
	return in.deliver(h, h.Key, "refs/tags/v1", "1111111111111111111111111111111111111111", time.Now(), false)
}

func (in *instance) DeliveryAt(h forge.Hook, ref, commit string, at time.Time) (forge.Delivery, error) {
	return in.deliver(h, h.Key, ref, commit, at, true)
}

func (in *instance) deliver(h forge.Hook, names repokey.Key, ref, commit string, at time.Time, isPush bool) (forge.Delivery, error) {
	id, err := strconv.ParseInt(string(h.Handle), 10, 64)
	if err != nil {
		return forge.Delivery{}, fmt.Errorf("gitlab-test: hook handle %q is not one this instance issued", h.Handle)
	}
	in.mu.Lock()
	record, ok := in.hooks[id]
	in.mu.Unlock()
	if !ok {
		return forge.Delivery{}, fmt.Errorf("gitlab-test: no hook %q to deliver to", h.Handle)
	}

	var payload deliveryPayload
	payload.ObjectKind, payload.EventName = "push", "push"
	if !isPush {
		payload.ObjectKind, payload.EventName = "tag_push", "tag_push"
	}
	payload.Ref = ref
	payload.After = commit
	payload.Before = "0000000000000000000000000000000000000000"
	payload.UserUsername = "alice"
	payload.TotalCommitsCount = 1
	// checkout_sha is null on a deletion, which is exactly why the driver falls
	// back to `after` — and why `after` being git's all-zero sentinel is what a
	// deletion looks like from here.
	if strings.Trim(commit, "0") != "" {
		checkout := commit
		payload.CheckoutSHA = &checkout
	}
	payload.Project.Name = names.Name
	payload.Project.PathWithNamespace = names.FullName()
	payload.Project.GitHTTPURL = in.base + "/" + names.FullName() + ".git"
	payload.Project.GitSSHURL = "ssh://git@" + in.host + "/" + names.FullName() + ".git"
	payload.Project.DefaultBranch = "main"

	body, err := json.Marshal(payload)
	if err != nil {
		return forge.Delivery{}, err
	}

	// The instance holds the WIRE STRING, not the secret, and signs with what it
	// was actually given — so a driver that re-encoded the wire on the way in
	// shows up as a delivery nothing can verify rather than as nothing at all.
	secret := h.Secret
	if want, err := h.Scheme.Wire(h.Secret); err != nil || want != record.wire {
		secret = forge.SecretFrom(padded(record.wire))
	}
	header, err := forge.Sign(h.Scheme, secret, body, "msg_"+string(h.Handle), at)
	if err != nil {
		return forge.Delivery{}, err
	}
	// GitLab names the event in prose, with a space and a capital.
	if isPush {
		header.Set("X-Gitlab-Event", "Push Hook")
	} else {
		header.Set("X-Gitlab-Event", "Tag Push Hook")
	}
	header.Set("X-Gitlab-Instance", in.base)
	return forge.Delivery{Header: header, Body: body}, nil
}

func padded(s string) []byte {
	b := make([]byte, forge.SecretSize)
	copy(b, s)
	return b
}

func (in *instance) lastHookWire() string {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.lastWire
}

func (in *instance) lastDeployKeyReadOnly() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return !in.lastCanPush
}

func (in *instance) hookInstalled(h forge.Handle) bool {
	id, err := strconv.ParseInt(string(h), 10, 64)
	if err != nil {
		return false
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	_, ok := in.hooks[id]
	return ok
}

func (in *instance) deployKeyInstalled(h forge.Handle) bool {
	id, err := strconv.ParseInt(string(h), 10, 64)
	if err != nil {
		return false
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	_, ok := in.deployKeys[id]
	return ok
}
