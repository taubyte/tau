package dream

import (
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

	DreamApiPort   = 1421
	DreamApiListen = func() string { return fmt.Sprintf("%s:%d", DefaultHost, DreamApiPort) }

	DefaultHost             string = "127.0.0.1"
	DefaultP2PListenFormat  string = "/ip4/" + DefaultHost + "/tcp/%d"
	DefaultHTTPListenFormat string = "%s://" + DefaultHost + ":%d"

	// Default multiverse name (must be lowercase)
	DefaultMultiverseName = "default"

	// Default universe name (must be lowercase)
	DefaultUniverseName = "default"

	// Default universe cache folder
	DefaultMultiverseCacheFolder = ".cache/dream"

	// Default client name
	DefaultClientName = "client"

	// Valid sub-binds for services
	ValidSubBinds = []string{"http", "p2p", "dns", "https", "verbose", "copies"}

	MeshTimeout = 30 * time.Second

	// Disk usage cache configuration
	DiskUsageCacheTimeout = 5 * time.Second

	startAllDefaultSimple = "client"

	logger = log.Logger("tau.dream")
)

// TODO: This should be generated
var FixtureMap = map[string]FixtureDefinition{
	"setBranch": {
		Description: "Set the git branch the services build from",
		ImportRef:   "libdream/common/fixtures",
		Variables: []FixtureVariable{
			{
				Name:        "name",
				Alias:       "n",
				Description: "Branch name",
				Required:    true,
			},
		},
	},
	"pushAll": {
		Description: "Build every repo Dream knows, as if each got a GitHub push",
		ImportRef:   "patrick",
		Variables: []FixtureVariable{
			{
				Name:        "project-id",
				Alias:       "pid",
				Description: "Only this project (default: every project)",
				Required:    false,
			},
			{
				Name:        "branch",
				Alias:       "b",
				Description: fmt.Sprintf("Branch to build (default: %v)", commonSpec.DefaultBranches[0]),
				Required:    false,
			},
			{
				Name:        "path",
				Alias:       "p",
				Description: "Absolute path to local project root",
				Required:    false,
			},
			{
				Name:        "force",
				Alias:       "f",
				Description: "true: rebuild every repo, even those whose commit already built",
				Required:    false,
			},
		},
	},
	"pushConfig": {
		Description: "pushes into config repo",
		ImportRef:   "patrick",
		Internal:    true,
		Variables: []FixtureVariable{
			{
				Name:        "path",
				Alias:       "p",
				Description: "Absolute path to local repo directory",
				Required:    false,
			},
		},
	},
	"pushCode": {
		Description: "pushes into code repo",
		ImportRef:   "patrick",
		Internal:    true,
		Variables: []FixtureVariable{
			{
				Name:        "path",
				Alias:       "p",
				Description: "Absolute path to local repo directory",
				Required:    false,
			},
		},
	},
	"pushWebsite": {
		Description: "pushes website repo",
		ImportRef:   "patrick",
		Internal:    true,
		Variables: []FixtureVariable{
			{
				Name:        "path",
				Alias:       "p",
				Description: "Absolute path to local repo directory",
				Required:    false,
			},
		},
	},
	"pushLibrary": {
		Description: "pushes library repo",
		ImportRef:   "patrick",
		Internal:    true,
		Variables: []FixtureVariable{
			{
				Name:        "path",
				Alias:       "p",
				Description: "Absolute path to local repo directory",
				Required:    false,
			},
		},
	},
	"attachDomain": {
		Description: "attaches default FQDN",
		ImportRef:   "substrate",
		Internal:    true,
	},
	"clearRepos": {
		Description: "delete all unused repos",
		ImportRef:   "dream-test/fixtures",
		Internal:    true,
	},
	"attachPlugin": {
		Description: "Load a VM-Orbit plugin binary into substrate",
		ImportRef:   "substrate",
		Variables: []FixtureVariable{
			{
				Name:        "paths",
				Description: "Plugin binary paths, comma separated",
				Alias:       "p",
				Required:    true,
			},
		},
	},
	"pushSpecific": {
		Description: "Build one repo, as if it got a GitHub push",
		ImportRef:   "patrick",
		Variables: []FixtureVariable{
			{
				Name:        "repository-id",
				Alias:       "rid",
				Description: "GitHub repository id (number)",
				Required:    true,
			},
			{
				Name:        "repository-fullname",
				Alias:       "fn",
				Description: "GitHub owner/name, e.g. taubyte-test/tb_repo",
				Required:    true,
			},
			{
				Name:        "project-id",
				Alias:       "pid",
				Description: "Project id (default: the repo's project)",
				Required:    false,
			},
			{
				Name:        "branch", // TODO : make this variable a slice
				Alias:       "b",
				Description: fmt.Sprintf("Defaults to %v", commonSpec.DefaultBranches),
				Required:    false,
			},
			{
				Name:        "path",
				Alias:       "p",
				Description: "Absolute path to local repo directory",
				Required:    false,
			},
		},
	},
	"attachProdProject": {
		Description: "Attach a production project to dream",
		ImportRef:   "dream-test/fixtures", // TODO should this fixture be in tns?
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
		Description: "Import a production project to dream and push all the repos",
		ImportRef:   "dream-test/fixtures", // TODO should this fixture be in tns?
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
