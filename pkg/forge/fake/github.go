package fake

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// github.go is a served forge answering in GitHub's REST dialect, so the real github
// driver can be bound to its host: every route that driver asks except the app's,
// each answered by the same faces the in-memory driver hands out. What the faces
// refuse is answered with the status GitHub would use.

// githubAPI is where an Enterprise instance serves its API.
const githubAPI = "/api/v3/"

// githubPage is GitHub's largest page, which is what the driver asks for.
const githubPage = 100

func (c *core) github() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /user", c.ghUser)
	mux.HandleFunc("GET /user/repos", c.ghList)
	mux.HandleFunc("POST /user/repos", func(w http.ResponseWriter, r *http.Request) { c.ghCreate(w, r, Login) })
	mux.HandleFunc("POST /orgs/{org}/repos", func(w http.ResponseWriter, r *http.Request) { c.ghCreate(w, r, r.PathValue("org")) })
	mux.HandleFunc("GET /repos/{owner}/{name}", c.ghRepository)
	mux.HandleFunc("DELETE /repos/{owner}/{name}", c.ghDelete)
	mux.HandleFunc("POST /repos/{owner}/{name}/keys", c.ghInstallKey)
	mux.HandleFunc("GET /repos/{owner}/{name}/keys", c.ghListKeys)
	mux.HandleFunc("DELETE /repos/{owner}/{name}/keys/{id}", c.ghRemoveKey)
	mux.HandleFunc("POST /repos/{owner}/{name}/hooks", c.ghRegisterHook)
	mux.HandleFunc("GET /repos/{owner}/{name}/hooks", c.ghListHooks)
	mux.HandleFunc("DELETE /repos/{owner}/{name}/hooks/{id}", c.ghRemoveHook)
	return mux
}

// ghClient is the faces bound to the request's bearer token.
func (c *core) ghClient(r *http.Request) forge.Client {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	client, err := newClient(c, token)
	if err != nil {
		panic("fake: a shape New accepted has no client: " + err.Error())
	}
	return client
}

// ghKey is the repository a request's path names on this host.
func (c *core) ghKey(w http.ResponseWriter, r *http.Request) (repokey.Key, bool) {
	k, err := repokey.Parse("https://" + c.shape.Host + "/" + r.PathValue("owner") + "/" + r.PathValue("name") + ".git")
	if err != nil {
		ghFail(w, http.StatusNotFound)
		return repokey.Key{}, false
	}
	return k, true
}

// ghRefused answers err with the status GitHub gives it.
func ghRefused(w http.ResponseWriter, err error) {
	var credential *forge.CredentialError
	var missing *forge.NotFoundError
	var refused interface{ Refused() bool }
	switch {
	case errors.As(err, &credential):
		ghFail(w, http.StatusUnauthorized)
	case errors.As(err, &missing):
		ghFail(w, http.StatusNotFound)
	case errors.As(err, &refused) && refused.Refused():
		ghFail(w, http.StatusForbidden)
	default:
		ghFail(w, http.StatusBadGateway)
	}
}

func ghFail(w http.ResponseWriter, status int) {
	ghAnswer(w, status, map[string]string{"message": http.StatusText(status)})
}

func ghAnswer(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// ghPage is the page of all the request asks for.
func ghPage[T any](r *http.Request, all []T) []T {
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	start := (page - 1) * githubPage
	if start >= len(all) {
		return []T{}
	}
	return all[start:min(start+githubPage, len(all))]
}

type ghPermissions struct {
	Admin    bool `json:"admin"`
	Maintain bool `json:"maintain"`
	Push     bool `json:"push"`
	Pull     bool `json:"pull"`
}

type ghRepo struct {
	FullName      string        `json:"full_name"`
	CloneURL      string        `json:"clone_url"`
	SSHURL        string        `json:"ssh_url"`
	DefaultBranch string        `json:"default_branch"`
	Private       bool          `json:"private"`
	Permissions   ghPermissions `json:"permissions"`
}

func ghRepoOf(repo forge.Repository, access forge.Access) ghRepo {
	return ghRepo{
		FullName:      repo.Key.FullName(),
		CloneURL:      repo.HTTPS,
		SSHURL:        repo.SSH,
		DefaultBranch: repo.DefaultBranch,
		Private:       repo.Private,
		Permissions: ghPermissions{
			Admin:    access >= forge.AccessAdmin,
			Maintain: access >= forge.AccessAdmin,
			Push:     access >= forge.AccessWrite,
			Pull:     access >= forge.AccessRead,
		},
	}
}

type ghUserJSON struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

func (c *core) ghUser(w http.ResponseWriter, r *http.Request) {
	// Nobody is the current user of an anonymous request.
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		ghFail(w, http.StatusUnauthorized)
		return
	}
	me, err := c.ghClient(r).Whoami(r.Context())
	if err != nil {
		ghRefused(w, err)
		return
	}
	id, err := strconv.ParseInt(me.Subject, 10, 64)
	if err != nil {
		ghFail(w, http.StatusBadGateway)
		return
	}
	ghAnswer(w, http.StatusOK, ghUserJSON{Login: me.Login, ID: id, Email: me.Email})
}

func (c *core) ghRepository(w http.ResponseWriter, r *http.Request) {
	k, ok := c.ghKey(w, r)
	if !ok {
		return
	}
	client := c.ghClient(r)
	repo, err := client.Repository(r.Context(), k)
	if err != nil {
		ghRefused(w, err)
		return
	}
	access, err := client.Access(r.Context(), k)
	if err != nil {
		ghRefused(w, err)
		return
	}
	ghAnswer(w, http.StatusOK, ghRepoOf(repo, access))
}

func (c *core) ghList(w http.ResponseWriter, r *http.Request) {
	lister, ok := c.ghClient(r).(forge.Lister)
	if !ok {
		ghFail(w, http.StatusNotFound)
		return
	}
	var all []ghRepo
	err := lister.ListRepositories(r.Context(), func(repo forge.Repository) bool {
		all = append(all, ghRepoOf(repo, forge.AccessRead))
		return true
	})
	if err != nil {
		ghRefused(w, err)
		return
	}
	ghAnswer(w, http.StatusOK, ghPage(r, all))
}

type ghCreateRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Private     bool   `json:"private"`
}

func (c *core) ghCreate(w http.ResponseWriter, r *http.Request, owner string) {
	creator, ok := c.ghClient(r).(forge.Creator)
	if !ok {
		ghFail(w, http.StatusNotFound)
		return
	}
	var in ghCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		ghFail(w, http.StatusUnprocessableEntity)
		return
	}
	k, err := repokey.Parse("https://" + c.shape.Host + "/" + owner + "/" + in.Name + ".git")
	if err != nil {
		ghFail(w, http.StatusUnprocessableEntity)
		return
	}
	repo, err := creator.Create(r.Context(), k, forge.CreateOptions{Description: in.Description, Private: in.Private})
	if err != nil {
		ghRefused(w, err)
		return
	}
	ghAnswer(w, http.StatusCreated, ghRepoOf(repo, forge.AccessAdmin))
}

func (c *core) ghDelete(w http.ResponseWriter, r *http.Request) {
	k, ok := c.ghKey(w, r)
	if !ok {
		return
	}
	deleter, ok := c.ghClient(r).(forge.Deleter)
	if !ok {
		ghFail(w, http.StatusNotFound)
		return
	}
	if err := deleter.Delete(r.Context(), k); err != nil {
		ghRefused(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type ghID struct {
	ID int64 `json:"id"`
}

// ghHandle is a handle this forge minted as the number GitHub gives a resource.
func ghHandle(w http.ResponseWriter, h forge.Handle) (int64, bool) {
	id, err := strconv.ParseInt(string(h), 10, 64)
	if err != nil {
		ghFail(w, http.StatusBadGateway)
		return 0, false
	}
	return id, true
}

type ghKeyRequest struct {
	Title string `json:"title"`
	Key   string `json:"key"`
}

type ghKeyJSON struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Key   string `json:"key"`
}

func (c *core) ghKeys(w http.ResponseWriter, r *http.Request) (forge.DeployKeys, repokey.Key, bool) {
	k, ok := c.ghKey(w, r)
	if !ok {
		return nil, k, false
	}
	keys, ok := c.ghClient(r).(forge.DeployKeys)
	if !ok {
		ghFail(w, http.StatusNotFound)
	}
	return keys, k, ok
}

func (c *core) ghInstallKey(w http.ResponseWriter, r *http.Request) {
	keys, k, ok := c.ghKeys(w, r)
	if !ok {
		return
	}
	var in ghKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		ghFail(w, http.StatusUnprocessableEntity)
		return
	}
	h, err := keys.InstallDeployKey(r.Context(), k, in.Title, in.Key)
	if err != nil {
		ghRefused(w, err)
		return
	}
	if id, ok := ghHandle(w, h); ok {
		ghAnswer(w, http.StatusCreated, ghID{ID: id})
	}
}

func (c *core) ghListKeys(w http.ResponseWriter, r *http.Request) {
	keys, k, ok := c.ghKeys(w, r)
	if !ok {
		return
	}
	installed, err := keys.ListDeployKeys(r.Context(), k)
	if err != nil {
		ghRefused(w, err)
		return
	}
	all := make([]ghKeyJSON, 0, len(installed))
	for _, key := range installed {
		id, ok := ghHandle(w, key.Handle)
		if !ok {
			return
		}
		all = append(all, ghKeyJSON{ID: id, Title: key.Title, Key: key.PublicKey})
	}
	ghAnswer(w, http.StatusOK, ghPage(r, all))
}

func (c *core) ghRemoveKey(w http.ResponseWriter, r *http.Request) {
	keys, k, ok := c.ghKeys(w, r)
	if !ok {
		return
	}
	if err := keys.RemoveDeployKey(r.Context(), k, forge.Handle(r.PathValue("id"))); err != nil {
		ghRefused(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type ghHookConfig struct {
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"`
}

type ghHookRequest struct {
	Config ghHookConfig `json:"config"`
}

type ghHookJSON struct {
	ID     int64        `json:"id"`
	Config ghHookConfig `json:"config"`
}

func (c *core) ghHooks(w http.ResponseWriter, r *http.Request) (forge.PushHooks, repokey.Key, bool) {
	k, ok := c.ghKey(w, r)
	if !ok {
		return nil, k, false
	}
	hooks, ok := c.ghClient(r).(forge.PushHooks)
	if !ok {
		ghFail(w, http.StatusNotFound)
	}
	return hooks, k, ok
}

func (c *core) ghRegisterHook(w http.ResponseWriter, r *http.Request) {
	hooks, k, ok := c.ghHooks(w, r)
	if !ok {
		return
	}
	var in ghHookRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		ghFail(w, http.StatusUnprocessableEntity)
		return
	}
	callback, err := url.Parse(in.Config.URL)
	if err != nil {
		ghFail(w, http.StatusUnprocessableEntity)
		return
	}
	h, err := hooks.RegisterPushHook(r.Context(), k, callback, in.Config.Secret)
	if err != nil {
		ghRefused(w, err)
		return
	}
	if id, ok := ghHandle(w, h); ok {
		ghAnswer(w, http.StatusCreated, ghID{ID: id})
	}
}

func (c *core) ghListHooks(w http.ResponseWriter, r *http.Request) {
	hooks, k, ok := c.ghHooks(w, r)
	if !ok {
		return
	}
	installed, err := hooks.ListPushHooks(r.Context(), k)
	if err != nil {
		ghRefused(w, err)
		return
	}
	all := make([]ghHookJSON, 0, len(installed))
	for _, hook := range installed {
		id, ok := ghHandle(w, hook.Handle)
		if !ok {
			return
		}
		all = append(all, ghHookJSON{ID: id, Config: ghHookConfig{URL: hook.Callback}})
	}
	ghAnswer(w, http.StatusOK, ghPage(r, all))
}

func (c *core) ghRemoveHook(w http.ResponseWriter, r *http.Request) {
	hooks, k, ok := c.ghHooks(w, r)
	if !ok {
		return
	}
	if err := hooks.RemovePushHook(r.Context(), k, forge.Handle(r.PathValue("id"))); err != nil {
		ghRefused(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
