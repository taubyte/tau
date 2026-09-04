package seer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-github/v71/github"
	"github.com/spf13/afero"
	"github.com/taubyte/tau/core/p2p/keypair"
	authIface "github.com/taubyte/tau/core/services/auth"
	tnsIface "github.com/taubyte/tau/core/services/tns"
	projectSchema "github.com/taubyte/tau/legacy/schema/project"
	commonSpec "github.com/taubyte/tau/legacy/specs/common"
	"github.com/taubyte/tau/legacy/specs/methods"
	structureSpec "github.com/taubyte/tau/legacy/specs/structure"
	"github.com/taubyte/tau/p2p/peer"
	tauConfig "github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/pkg/git/repokey"
	httpPkg "github.com/taubyte/tau/pkg/http"
	httpBasic "github.com/taubyte/tau/pkg/http/basic"
	authService "github.com/taubyte/tau/services/auth"
	"gotest.tools/v3/assert"
)

const (
	gwVictimProject = "PROJECT_VICTIM"
	gwVictimConfig  = "github.com/victim/config"
	gwVictimCode    = "github.com/victim/code"
	gwVictimAsset   = "QmVictimAsset"
	gwStrangerAsset = "QmStrangerAsset"
	gwFunctionID    = "FUNCTION_VICTIM"
)

// gwGitHubClient is one caller's GitHub client: the repositories it can write
// to, and nothing else. Everything else still resolves — that is what a public
// repository does — but carries no write permission.
type gwGitHubClient struct {
	authService.GitHubClient
	login    string
	writable map[string]bool
	cur      *github.Repository
}

func (c *gwGitHubClient) Me() *github.User { return &github.User{Login: &c.login} }

func (c *gwGitHubClient) Get(key repokey.Key) error {
	id := key.String()
	c.cur = &github.Repository{
		FullName:    github.Ptr(key.FullName()),
		Permissions: map[string]bool{"pull": true, "push": c.writable[id], "admin": c.writable[id]},
	}
	return nil
}

func (c *gwGitHubClient) Cur() *github.Repository { return c.cur }

type gwAuthClient struct {
	authIface.Client
	projects map[string]*authIface.Project
}

func (c *gwAuthClient) Projects() authIface.Projects { return gwProjects{projects: c.projects} }

type gwProjects struct {
	authIface.Projects
	projects map[string]*authIface.Project
}

func (p gwProjects) Get(id string) *authIface.Project { return p.projects[id] }

type gwRepository struct{ id string }

func (r gwRepository) PrivateKey() string { return "" }
func (r gwRepository) Id() string         { return r.id }

// gwTns answers only what the asset walk asks of it: the cid stored at each
// asset path. Everything the project publishes is in `assets`, and nothing else
// resolves — which is what makes a foreign cid a miss rather than a hit.
type gwTns struct {
	tnsIface.Client
	assets map[string]string
}

func (m *gwTns) Fetch(path tnsIface.Path) (tnsIface.Object, error) {
	cid, ok := m.assets[path.String()]
	if !ok {
		return nil, fmt.Errorf("no value at `%s`", path.String())
	}
	return &gwTnsObject{value: cid}, nil
}

// Simple is only reached when a request gets past the auth block, which is what
// the pre-fix route table let anonymous callers do.
func (m *gwTns) Simple() tnsIface.SimpleIface { return gwSimple{} }

type gwSimple struct {
	tnsIface.SimpleIface
}

func (gwSimple) Project(string, ...string) (interface{}, error) {
	return nil, errors.New("no project object stored")
}

type gwTnsObject struct {
	tnsIface.Object
	value interface{}
}

func (o *gwTnsObject) Interface() interface{} { return o.value }

type gwNode struct {
	peer.Node
}

func (n *gwNode) Context() context.Context { return context.Background() }

func (n *gwNode) GetFile(ctx context.Context, id string) (peer.ReadSeekCloser, error) {
	return nil, errors.New("no such file")
}

// gwProject is a project holding exactly one function, whose asset cid is
// published under that project's id — the shape the compiler leaves in TNS.
func gwProject(t *testing.T) (projectSchema.Project, *gwTns) {
	t.Helper()

	prj, err := projectSchema.Open(projectSchema.VirtualFS(afero.NewMemMapFs(), "/"))
	assert.NilError(t, err)
	assert.NilError(t, prj.Set(true, projectSchema.Id(gwVictimProject), projectSchema.Name("victim")))

	fn, err := prj.Function("test_function", "")
	assert.NilError(t, err)
	assert.NilError(t, fn.SetWithStruct(true, &structureSpec.Function{
		Id:     gwFunctionID,
		Name:   "test_function",
		Type:   "http",
		Method: "GET",
		Paths:  []string{"/ping"},
		Source: ".",
		Call:   "ping",
	}))

	assets := make(map[string]string)
	for _, branch := range commonSpec.DefaultBranches {
		path, err := methods.GetTNSAssetPath(gwVictimProject, gwFunctionID, branch)
		assert.NilError(t, err)
		assets[path.String()] = gwVictimAsset
	}

	return prj, &gwTns{assets: assets}
}

func gwService(t *testing.T) (*Service, string) {
	t.Helper()

	return &Service{
		node:    &gwNode{},
		devMode: false,
		config: tauConfigOrDie(t,
			tauConfig.WithDevMode(false),
			tauConfig.WithNetworkFqdn("example.com"),
			tauConfig.WithPrivateKey(keypair.NewRaw()),
		),
		authClient: &gwAuthClient{
			projects: map[string]*authIface.Project{
				gwVictimProject: gwProjectRecord(gwVictimProject, gwVictimConfig, gwVictimCode),
			},
		},
	}, "seer.tau.example.com"
}

func gwProjectRecord(id, config, code string) *authIface.Project {
	prj := &authIface.Project{Id: id}
	prj.Git.Config = gwRepository{id: config}
	prj.Git.Code = gwRepository{id: code}
	return prj
}

func tauConfigOrDie(t *testing.T, opts ...tauConfig.Option) tauConfig.Config {
	t.Helper()
	cfg, err := tauConfig.New(opts...)
	assert.NilError(t, err)
	return cfg
}

// TestGatewayRoutesRefuseUnauthenticated is the reproduction: before the fix
// neither /config/{projectId} nor /download/{projectId}/{assetCID} carried an
// Auth block, so both answered anybody who asked.
func TestGatewayRoutesRefuseUnauthenticated(t *testing.T) {
	for _, path := range []string{
		"/config/" + gwVictimProject,
		"/download/" + gwVictimProject + "/" + gwVictimAsset,
	} {
		t.Run(path, func(t *testing.T) {
			srv, host := gwService(t)
			_, tns := gwProject(t)
			srv.tns = tns

			httpSrv, err := httpBasic.New(context.Background())
			assert.NilError(t, err)
			srv.http = httpSrv
			srv.setupTNSGatewayHTTPRoutes()

			req := httptest.NewRequest("GET", "http://"+host+path, nil)
			req.Host = host
			w := httptest.NewRecorder()
			httpSrv.Router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusUnauthorized, w.Code, "unauthenticated request was answered: %s", w.Body.String())
		})
	}
}

// TestGatewayObjectAuthorizationRefusesStranger is the other direction: a
// caller with a perfectly good token, and repositories of their own, still may
// not read another project's configuration or assets.
func TestGatewayObjectAuthorizationRefusesStranger(t *testing.T) {
	srv, _ := gwService(t)

	stranger := &gwGitHubClient{login: "stranger", writable: map[string]bool{
		"github.com/stranger/config": true,
		"github.com/stranger/code":   true,
	}}
	owner := &gwGitHubClient{login: "victim", writable: map[string]bool{
		gwVictimConfig: true,
		gwVictimCode:   true,
	}}

	t.Run("stranger refused", func(t *testing.T) {
		ctx := newGwContext(map[string]interface{}{
			"projectId":    gwVictimProject,
			"GithubClient": authService.GitHubClient(stranger),
		})
		assert.Error(t, srv.authorizeProjectVariable(ctx), authService.ErrProjectNotFound.Error())
	})

	t.Run("owner allowed", func(t *testing.T) {
		ctx := newGwContext(map[string]interface{}{
			"projectId":    gwVictimProject,
			"GithubClient": authService.GitHubClient(owner),
		})
		assert.NilError(t, srv.authorizeProjectVariable(ctx))
	})
}

// TestDownloadAssetRefusesForeignCid is the defect the deleted TODO named: a cid
// is an address, not a credential. A caller authorized for their own project
// must not be able to spend that authorization on somebody else's asset.
func TestDownloadAssetRefusesForeignCid(t *testing.T) {
	srv, _ := gwService(t)
	prj, tns := gwProject(t)
	srv.tns = tns

	assert.Assert(t, srv.projectPublishes(prj, gwVictimAsset), "the project's own asset was not recognised")
	assert.Assert(t, !srv.projectPublishes(prj, gwStrangerAsset), "a cid the project never published was accepted")

	// And the handler asks before it fetches: with no project object to resolve
	// the cid against, there is no answer that lets the bytes out.
	ctx := newGwContext(map[string]interface{}{
		"projectId": gwVictimProject,
		"assetCID":  gwVictimAsset,
	})
	_, err := srv.downloadAsset(ctx)
	assert.ErrorContains(t, err, "fetching project object failed")
}

type gwContext struct {
	variables map[string]interface{}
	request   *http.Request
}

func newGwContext(vars map[string]interface{}) *gwContext {
	return &gwContext{
		variables: vars,
		request:   httptest.NewRequest("GET", "http://seer.tau.example.com/", nil),
	}
}

func (c *gwContext) HandleWith(handler httpPkg.Handler) error { return nil }
func (c *gwContext) HandleAuth(httpPkg.Handler) error         { return nil }
func (c *gwContext) HandleCleanup(httpPkg.Handler) error      { return nil }
func (c *gwContext) Request() *http.Request                   { return c.request }
func (c *gwContext) Writer() http.ResponseWriter              { return nil }
func (c *gwContext) ParseBody(obj interface{}) error          { return nil }
func (c *gwContext) RawResponse() bool                        { return false }
func (c *gwContext) SetRawResponse(bool)                      {}
func (c *gwContext) Variables() map[string]interface{}        { return c.variables }
func (c *gwContext) SetVariable(key string, val interface{})  { c.variables[key] = val }
func (c *gwContext) Body() []byte                             { return nil }
func (c *gwContext) SetBody([]byte)                           {}
func (c *gwContext) GetStringArrayVariable(string) ([]string, error) {
	return nil, errors.New("not implemented")
}
func (c *gwContext) GetStringMapVariable(string) (map[string]interface{}, error) {
	return nil, errors.New("not implemented")
}
func (c *gwContext) GetIntVariable(string) (int, error) { return 0, errors.New("not implemented") }

func (c *gwContext) GetStringVariable(key string) (string, error) {
	if v, ok := c.variables[key].(string); ok {
		return v, nil
	}
	return "", fmt.Errorf("variable `%s` not found", key)
}
