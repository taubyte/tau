package registry

import (
	"fmt"

	commonSpec "github.com/taubyte/go-specs/common"
)

type FixtureVariable struct {
	Name        string
	Alias       string
	Description string
	Required    bool
}

type FixtureDefinition struct {
	Description string
	ImportRef   string
	Variables   []FixtureVariable
	BlockCLI    bool
}

var FixtureMap = map[string]FixtureDefinition{
	"setBranch": {
		Description: "set the default branch for protocols to resolve",
		ImportRef:   "libdream/common/fixtures",
		Variables: []FixtureVariable{
			{
				Name:     "name",
				Alias:    "n",
				Required: true,
			},
		},
	},
	"createProjectWithJobs": {Description: "creates jobs for code and config repos", ImportRef: "patrick"},
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
	"pushConfig":   {Description: "pushes into config repo", ImportRef: "patrick"},
	"pushCode":     {Description: "pushes into code repo", ImportRef: "patrick"},
	"pushWebsite":  {Description: "pushes website repo", ImportRef: "patrick"},
	"pushLibrary":  {Description: "pushes library repo", ImportRef: "patrick"},
	"attachDomain": {Description: "attaches default FQDN", ImportRef: "substrate"},
	"clearRepos":   {Description: "delete all unused repos", ImportRef: "dreamland-test/fixtures"},
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
				Name:        "branch",
				Alias:       "b",
				Description: fmt.Sprintf("Defaults to %s", commonSpec.DefaultBranch),
				Required:    false,
			},
		},
	},
	"attachProdProject": {
		Description: "Attach a production project to dreamland",
		ImportRef:   "dreamland-test/fixtures", // TODO should this fixture be in tns?
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
				Description: fmt.Sprintf("Defaults to %s", commonSpec.DefaultBranch),
				Required:    false,
			},
		},
	},
	"fakeProject":   {Description: "Pushes the internal project to tns", ImportRef: "tau/libdream/common/fixtures"},
	"injectProject": {Description: "Pass in a *projectSchema.Project to inject it into tns", ImportRef: "tau/libdream/common/fixtures", BlockCLI: true},
	"compileFor": {
		Description: "pushes specific repos",
		ImportRef:   "monkey/fixtures/compile",
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
				Description: fmt.Sprintf("Defaults to %s", commonSpec.DefaultBranch),
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
				Description: fmt.Sprintf("Defaults to %s", commonSpec.DefaultBranch),
				Required:    false,
			},
		},
	},
}
