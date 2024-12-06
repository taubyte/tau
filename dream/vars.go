package dream

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ipfs/go-log/v2"
	commonSpec "github.com/taubyte/tau/pkg/specs/common"
)

// TODO: Need to verify which vars need to be exported
var (
	fixtures     map[string]FixtureHandler
	fixturesLock sync.RWMutex

	//buffer between protocol ports
	portBuffer = 21
	portStart  = 100

	Ports map[string]int

	DreamlandApiListen = DefaultHost + ":1421"

	DefaultHost             string = "127.0.0.1"
	DefaultP2PListenFormat  string = "/ip4/" + DefaultHost + "/tcp/%d"
	DefaultHTTPListenFormat string = "%s://" + DefaultHost + ":%d"

	BaseAfterStartDelay = 500  // Millisecond
	MaxAfterStartDelay  = 1000 // Millisecond
	MeshTimeout         = 5 * time.Second

	startAllDefaultSimple = "client"

	lastSimplePortAllocated     = 50
	lastSimplePortAllocatedLock sync.Mutex

	lastUniversePortShift     = 9000
	lastUniversePortShiftLock sync.Mutex

	maxUniverses     = 100
	portsPerUniverse = 100

	logger = log.Logger("tau.dreamland")

	universes      map[string]*Universe
	universesLock  sync.RWMutex
	multiverseCtx  context.Context
	multiverseCtxC context.CancelFunc
)

// TODO: This should be generated
var FixtureMap = map[string]FixtureDefinition{
	"setBranch": {
		Description: "set the default branch for services to resolve",
		ImportRef:   "libdream/common/fixtures",
		Variables: []FixtureVariable{
			{
				Name:     "name",
				Alias:    "n",
				Required: true,
			},
		},
	},
	"createProjectWithJobs": {
		Description: "creates jobs for code and config repos",
		ImportRef:   "patrick",
		Internal:    true,
	},
	"pushAll": {
		Description: "pushes all ",
		ImportRef:   "patrick",
		Variables: []FixtureVariable{
			{
				Name:     "project-id",
				Alias:    "pid",
				Required: false,
			},
			{
				Name:     "branch",
				Alias:    "b",
				Required: false,
			},
		},
	},
	"pushConfig": {
		Description: "pushes into config repo",
		ImportRef:   "patrick",
		Internal:    true,
	},
	"pushCode": {
		Description: "pushes into code repo",
		ImportRef:   "patrick",
		Internal:    true,
	},
	"pushWebsite": {
		Description: "pushes website repo",
		ImportRef:   "patrick",
		Internal:    true,
	},
	"pushLibrary": {
		Description: "pushes library repo",
		ImportRef:   "patrick",
		Internal:    true,
	},
	"attachDomain": {
		Description: "attaches default FQDN",
		ImportRef:   "substrate",
		Internal:    true,
	},
	"clearRepos": {
		Description: "delete all unused repos",
		ImportRef:   "dreamland-test/fixtures",
		Internal:    true,
	},
	"attachPlugin": {
		Description: "inject a plugin binary built using VM-Orbit",
		ImportRef:   "substrate",
		Variables: []FixtureVariable{
			{
				Name:        "paths",
				Description: "comma separated list of binary paths",
				Alias:       "p",
				Required:    true,
			},
		},
	},
	"pushSpecific": {
		Description: "pushes specific repos",
		ImportRef:   "patrick",
		Variables: []FixtureVariable{
			{
				Name:     "repository-id",
				Alias:    "rid",
				Required: true,
			},
			{
				Name:        "repository-fullname",
				Alias:       "fn",
				Description: "ex: taubyte-test/tb_repo",
				Required:    true,
			},
			{
				Name:        "project-id",
				Alias:       "pid",
				Description: "Defaults to the test project id",
				Required:    false,
			},
			{
				Name:        "branch", // TODO : make this variable a slice
				Alias:       "b",
				Description: fmt.Sprintf("Defaults to %v", commonSpec.DefaultBranches),
				Required:    false,
			},
		},
	},
	"attachProdProject": {
		Description: "Attach a production project to dreamland",
		ImportRef:   "dreamland-test/fixtures", // TODO should this fixture be in tns?
		Internal:    true,
		Variables: []FixtureVariable{
			{
				Name:        "project-id",
				Alias:       "pid",
				Description: "",
				Required:    true,
			},
			{
				Name:        "git-token",
				Alias:       "t",
				Description: "",
				Required:    true,
			},
		},
	},
	"importProdProject": {
		Description: "Import a production project to dreamland and push all the repos",
		ImportRef:   "dreamland-test/fixtures", // TODO should this fixture be in tns?
		Internal:    true,
		Variables: []FixtureVariable{
			{
				Name:        "project-id",
				Alias:       "pid",
				Description: "",
				Required:    true,
			},
			{
				Name:        "git-token",
				Alias:       "t",
				Description: "",
				Required:    true,
			},
			{
				Name:        "branch",
				Alias:       "b",
				Description: fmt.Sprintf("Defaults to %v", commonSpec.DefaultBranches),
				Required:    false,
			},
		},
	},
	"fakeProject": {
		Description: "Pushes the internal project to tns",
		ImportRef:   "tau/dream/fixtures",
		Internal:    true,
	},
	"injectProject": {
		Description: "Pass in a *projectSchema.Project to inject it into tns",
		ImportRef:   "tau/dream/fixtures",
		BlockCLI:    true,
		Internal:    true,
	},
	"compileFor": {
		Description: "pushes specific repos",
		ImportRef:   "monkey/fixtures/compile",
		Internal:    true,
		Variables: []FixtureVariable{
			{
				Name:        "project-id",
				Alias:       "pid",
				Description: "Defaults to the test project id",
				Required:    true,
			},
			{
				Name:        "application-id",
				Alias:       "app",
				Description: "",
				Required:    false,
			},
			{
				Name:        "resource-id",
				Alias:       "rid",
				Description: "",
				Required:    true,
			},
			{
				Name:        "branch",
				Alias:       "b",
				Description: fmt.Sprintf("Defaults to %v", commonSpec.DefaultBranches),
				Required:    false,
			},
			{
				Name:        "path",
				Alias:       "p",
				Description: "Can be a directory, go file, or a wasm file.  Defaults to a ping/pong wasm file",
				Required:    false,
			},
			{
				Name:        "call",
				Alias:       "c",
				Description: "",
				Required:    false,
			},
		},
	},
	"buildLocalProject": {
		Description: "pushes specific repos",
		ImportRef:   "monkey/fixtures/compile",
		Internal:    true,
		Variables: []FixtureVariable{
			{
				Name:        "config",
				Description: "Do build config",
				Required:    true,
			},
			{
				Name:        "code",
				Description: "Do build code",
				Required:    true,
			},
			{
				Name:        "path",
				Alias:       "p",
				Description: "path/to/taubyte/project",
				Required:    true,
			},
			{
				Name:        "branch",
				Alias:       "b",
				Description: fmt.Sprintf("Defaults to %v", commonSpec.DefaultBranches),
				Required:    false,
			},
		},
	},
}
