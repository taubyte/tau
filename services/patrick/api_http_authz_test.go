package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/google/go-github/v71/github"
	"github.com/taubyte/tau/core/p2p/keypair"
	authIface "github.com/taubyte/tau/core/services/auth"
	commonIface "github.com/taubyte/tau/core/services/patrick"
	tauConfig "github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/pkg/git/repokey"
	httpPkg "github.com/taubyte/tau/pkg/http"
	httpBasic "github.com/taubyte/tau/pkg/http/basic"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	authService "github.com/taubyte/tau/services/auth"
	"gotest.tools/v3/assert"
)

// authzGitHubClient is one caller's GitHub client: the repositories it can write
// to, and nothing else. A repository outside `writable` still resolves — that is
// what a public repository does — but carries no write permission, which is the
// distinction the whole rule turns on.
type authzGitHubClient struct {
	authService.GitHubClient
	login    string
	writable map[string]bool
	cur      *github.Repository
}

func (c *authzGitHubClient) Me() *github.User {
	return &github.User{Login: &c.login}
}

func (c *authzGitHubClient) Get(key repokey.Key) error {
	id := key.String()
	c.cur = &github.Repository{
		FullName:    github.Ptr(key.FullName()),
		Permissions: map[string]bool{"pull": true, "push": c.writable[id], "admin": c.writable[id]},
	}
	return nil
}

func (c *authzGitHubClient) Cur() *github.Repository { return c.cur }

// authzAuthClient answers "which repositories is this project linked to", which
// is all seer and patrick need from auth to ask the access question.
type authzAuthClient struct {
	authIface.Client
	projects map[string]*authIface.Project
}

func (c *authzAuthClient) Projects() authIface.Projects {
	return authzProjects{projects: c.projects}
}

type authzProjects struct {
	authIface.Projects
	projects map[string]*authIface.Project
}

func (p authzProjects) Get(id string) *authIface.Project { return p.projects[id] }

type authzRepository struct{ id string }

func (r authzRepository) PrivateKey() string { return "" }
func (r authzRepository) Id() string         { return r.id }

func authzProject(id, config, code string) *authIface.Project {
	prj := &authIface.Project{Id: id}
	prj.Git.Config = authzRepository{id: config}
	prj.Git.Code = authzRepository{id: code}
	return prj
}

const (
	authzVictimProject = "PROJECT_VICTIM"
	authzVictimConfig  = "github.com/victim/config"
	authzVictimCode    = "github.com/victim/code"
)

// authzService is patrick as it runs on the internet: not in dev mode, holding
// one archived job that belongs to the victim's project.
func authzService(t *testing.T) (*PatrickService, string) {
	t.Helper()

	mockFactory := mock.New()
	mockDB, err := mockFactory.New(nil, "/test", 0)
	assert.NilError(t, err)

	srv := &PatrickService{
		db:      mockDB,
		node:    &mockNodeWithGetFile{mockNode: &mockNode{}},
		devMode: false,
		config: testConfig(t,
			tauConfig.WithDevMode(false),
			tauConfig.WithNetworkFqdn("example.com"),
			tauConfig.WithPrivateKey(keypair.NewRaw()),
		),
		authClient: &authzAuthClient{
			// createTestJob's clone URL derives to testJobRepoKey; auth is the
			// only thing that maps a job's repository to a project, so it has
			// to say so here too.
			Client: &mockAuthClient{
				repos: map[string]mockRepo{testJobRepoKey: {projectID: authzVictimProject}},
			},
			projects: map[string]*authIface.Project{
				authzVictimProject: authzProject(authzVictimProject, authzVictimConfig, authzVictimCode),
			},
		},
	}

	job := createTestJob("victim-job")
	job.Status = commonIface.JobStatusSuccess
	job.AssetCid = map[string]string{"resource-1": "QmVictimAsset"}
	job.Logs = map[string]string{"resource-1": "QmVictimLog"}
	jobBytes, err := cbor.Marshal(job)
	assert.NilError(t, err)
	assert.NilError(t, srv.db.Put(context.Background(), "/archive/jobs/victim-job", jobBytes))
	assert.NilError(t, srv.db.Put(context.Background(), "/by/project/"+authzVictimProject+"/victim-job", []byte{}))
	assert.NilError(t, srv.indexJobLogs(context.Background(), job))

	return srv, "patrick.tau.example.com"
}

// serveRoutes registers the service's real route table on a real router and
// answers one request through it — auth block included, which is the half a
// handler-level test cannot see.
func serveRoutes(t *testing.T, srv *PatrickService, host, path string) *httptest.ResponseRecorder {
	t.Helper()

	httpSrv, err := httpBasic.New(context.Background())
	assert.NilError(t, err)
	srv.http = httpSrv
	srv.setupJobRoutes()

	req := httptest.NewRequest("GET", "http://"+host+path, nil)
	req.Host = host
	w := httptest.NewRecorder()
	httpSrv.Router.ServeHTTP(w, req)

	return w
}

// TestJobRoutesRefuseUnauthenticated is the reproduction: before the fix
// /download/{jobId}/{resourceId} carried no Auth block at all and handed the
// build artifact to anyone who asked.
func TestJobRoutesRefuseUnauthenticated(t *testing.T) {
	for _, path := range []string{
		"/download/victim-job/resource-1",
		"/jobs/" + authzVictimProject,
		"/job/victim-job",
	} {
		t.Run(path, func(t *testing.T) {
			srv, host := authzService(t)
			w := serveRoutes(t, srv, host, path)
			assert.Equal(t, http.StatusUnauthorized, w.Code, "unauthenticated request was answered: %s", w.Body.String())
		})
	}
}

// TestJobRoutesCarryObjectAuthorization pins the route table itself: every one
// of these paths names something a project owns — a project, a job, a log cid —
// so every one of them declares both halves of its auth block, and the object
// half is taken straight out of the table and asked.
//
// A validator alone is a token check, and a token says who is asking and never
// what they may see: that is precisely what left /jobs and /job open to any
// caller holding a token and somebody else's id. Asserting the validator is
// present cannot see that, because the vulnerable route had one too.
func TestJobRoutesCarryObjectAuthorization(t *testing.T) {
	stranger := &authzGitHubClient{login: "stranger", writable: map[string]bool{
		"github.com/stranger/config": true,
		"github.com/stranger/code":   true,
	}}
	owner := &authzGitHubClient{login: "victim", writable: map[string]bool{
		authzVictimConfig: true,
		authzVictimCode:   true,
	}}

	// What each path names, spelled the way the route's own variables are.
	for _, route := range []struct {
		path string
		vars map[string]interface{}
	}{
		{"/jobs/{projectId}", map[string]interface{}{"projectId": authzVictimProject}},
		{"/job/{jid}", map[string]interface{}{"jid": "victim-job"}},
		{"/download/{jobId}/{resourceId}", map[string]interface{}{"jobId": "victim-job", "resourceId": "resource-1"}},
		{"/cancel/{jid}", map[string]interface{}{"jid": "victim-job"}},
		{"/retry/{jid}", map[string]interface{}{"jid": "victim-job"}},
		{"/logs/{cid}", map[string]interface{}{"cid": "QmVictimLog"}},
	} {
		t.Run(route.path, func(t *testing.T) {
			srv, _ := authzService(t)

			captured := make(map[string]*httpPkg.RouteDefinition)
			srv.http = &capturingHTTPService{routes: captured}
			srv.setupJobRoutes()

			def, ok := captured[route.path]
			assert.Assert(t, ok, "route %s was not registered", route.path)
			assert.Assert(t, def.Auth.Validator != nil, "route %s authenticates nobody", route.path)
			assert.Assert(t, def.Auth.Object != nil,
				"route %s names an object and does not authorize it: a token proves who is asking, never what they may see", route.path)

			assert.Assert(t, def.Auth.Object(authzContext(route.vars, stranger)) != nil,
				"route %s served a caller with no claim on what its path names", route.path)
			assert.NilError(t, def.Auth.Object(authzContext(route.vars, owner)),
				"route %s refused the project that owns what its path names", route.path)
		})
	}
}

// authzContext is a request at one of these routes: the variables the path
// carries, and the GitHub client the token check would have built.
func authzContext(vars map[string]interface{}, client *authzGitHubClient) *mockHTTPContext {
	ctx := newMockHTTPContext()
	for key, value := range vars {
		ctx.SetVariable(key, value)
	}
	ctx.SetVariable("GithubClient", authService.GitHubClient(client))

	return ctx
}

// TestJobObjectAuthorizationRefusesStranger is the other direction: a caller who
// authenticated perfectly well, and has write access to repositories of their
// own, still may not read a job belonging to a project they have no business
// with. A token proves who you are, never what you may see.
func TestJobObjectAuthorizationRefusesStranger(t *testing.T) {
	srv, _ := authzService(t)

	stranger := &authzGitHubClient{login: "stranger", writable: map[string]bool{
		"github.com/stranger/config": true,
		"github.com/stranger/code":   true,
	}}
	owner := &authzGitHubClient{login: "victim", writable: map[string]bool{
		authzVictimConfig: true,
		authzVictimCode:   true,
	}}

	authorizeJob := srv.authorizeJobVariable("jobId")

	t.Run("stranger refused", func(t *testing.T) {
		ctx := newMockHTTPContext()
		ctx.SetVariable("jobId", "victim-job")
		ctx.SetVariable("GithubClient", authService.GitHubClient(stranger))
		assert.Error(t, authorizeJob(ctx), errJobNotFound.Error())
	})

	t.Run("owner allowed", func(t *testing.T) {
		ctx := newMockHTTPContext()
		ctx.SetVariable("jobId", "victim-job")
		ctx.SetVariable("GithubClient", authService.GitHubClient(owner))
		assert.NilError(t, authorizeJob(ctx))
	})

	t.Run("project route refuses stranger", func(t *testing.T) {
		ctx := newMockHTTPContext()
		ctx.SetVariable("projectId", authzVictimProject)
		ctx.SetVariable("GithubClient", authService.GitHubClient(stranger))
		assert.Error(t, srv.authorizeProjectVariable(ctx), authService.ErrProjectNotFound.Error())
	})

	t.Run("project route allows owner", func(t *testing.T) {
		ctx := newMockHTTPContext()
		ctx.SetVariable("projectId", authzVictimProject)
		ctx.SetVariable("GithubClient", authService.GitHubClient(owner))
		assert.NilError(t, srv.authorizeProjectVariable(ctx))
	})

	authorizeLog := srv.authorizeLogVariable("cid")

	t.Run("log cid refused to stranger", func(t *testing.T) {
		ctx := newMockHTTPContext()
		ctx.SetVariable("cid", "QmVictimLog")
		ctx.SetVariable("GithubClient", authService.GitHubClient(stranger))
		assert.Error(t, authorizeLog(ctx), errLogNotFound.Error())
	})

	t.Run("log cid allowed to owner", func(t *testing.T) {
		ctx := newMockHTTPContext()
		ctx.SetVariable("cid", "QmVictimLog")
		ctx.SetVariable("GithubClient", authService.GitHubClient(owner))
		assert.NilError(t, authorizeLog(ctx))
	})

	// The route used to serve any cid the node held to any caller with a token,
	// build artifacts and other projects' logs included. A cid no job ever
	// claimed is nobody's, so it is nobody's to read.
	t.Run("unclaimed cid refused to everyone", func(t *testing.T) {
		for _, client := range []*authzGitHubClient{owner, stranger} {
			ctx := newMockHTTPContext()
			ctx.SetVariable("cid", "QmSomeOtherContent")
			ctx.SetVariable("GithubClient", authService.GitHubClient(client))
			assert.Error(t, authorizeLog(ctx), errLogNotFound.Error())
		}
	})
}

// capturingHTTPService keeps the route definitions a service registers, so a
// test can read the auth block a route declares rather than only its path.
type capturingHTTPService struct {
	mockHTTPService
	routes map[string]*httpPkg.RouteDefinition
}

func (c *capturingHTTPService) GET(def *httpPkg.RouteDefinition)    { c.routes[def.Path] = def }
func (c *capturingHTTPService) POST(def *httpPkg.RouteDefinition)   { c.routes[def.Path] = def }
func (c *capturingHTTPService) PUT(def *httpPkg.RouteDefinition)    { c.routes[def.Path] = def }
func (c *capturingHTTPService) DELETE(def *httpPkg.RouteDefinition) { c.routes[def.Path] = def }
