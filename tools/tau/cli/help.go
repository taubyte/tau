package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/taubyte/tau/tools/tau/cli/commands/version"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/urfave/cli/v2"
)

const gettingStarted = `Build and deploy on a Taubyte cloud, local (Dream) or remote, from your terminal.

GETTING STARTED:
   1. tau login                               log in with your GitHub account
   2. tau select cloud --universe default     or --fqdn <your cloud>
   3. tau new project                         create a project and clone it
   4. tau push project --wait                 push it and follow the builds
   Stuck? tau doctor checks your setup and says how to fix it.`

var categories = map[string]string{
	"login": "Getting started", "select": "Getting started", "current": "Getting started", "clear": "Getting started", "doctor": "Getting started",
	"new": "Projects & resources", "edit": "Projects & resources", "delete": "Projects & resources", "list": "Projects & resources",
	"query": "Projects & resources", "import": "Projects & resources", "clone": "Projects & resources", "checkout": "Projects & resources", "validate": "Projects & resources",
	"push": "Build & deploy", "pull": "Build & deploy", "build": "Build & deploy", "status": "Build & deploy", "logs": "Build & deploy",
	"cancel": "Build & deploy", "retry": "Build & deploy", "run": "Build & deploy",
	"version": "Other", "autocomplete": "Other",
}

// topUsage fills the top-level commands that have no Usage of their own.
var topUsage = map[string]string{
	"login":    "Log in with GitHub, or switch between saved logins",
	"new":      "Create a project or a resource",
	"edit":     "Change a resource's settings",
	"delete":   "Delete a project or a resource",
	"query":    "Show the details of one item",
	"list":     "List projects, resources or builds",
	"select":   "Choose the cloud, project or application to work on",
	"clone":    "Clone a project or a resource's repository",
	"push":     "Commit and push local changes to GitHub",
	"pull":     "Pull the latest changes from GitHub",
	"checkout": "Switch repositories to a branch",
	"import":   "Add existing GitHub repositories to the selected cloud",
	"cancel":   "Cancel a build job",
	"retry":    "Run a build job again",
	"run":      "Run a function locally against one HTTP request",
	"clear":    "Unselect the project or application",
	"version":  "Print tau's version",
}

// verbUsage is the Usage of `tau <verb> <kind>`; %s is the kind.
var verbUsage = map[string]string{
	"new":      "Create a %s",
	"edit":     "Change a %s's settings",
	"delete":   "Delete a %s",
	"query":    "Show a %s's settings (--list for all)",
	"list":     "List every %s in the selected project",
	"select":   "Select a %s",
	"clone":    "Clone a %s's repository",
	"push":     "Commit and push a %s's repository",
	"pull":     "Pull a %s's repository",
	"checkout": "Switch a %s's repository to a branch",
	"import":   "Attach an existing GitHub repository as a %s",
	"run":      "Run a %s locally against one HTTP request",
	"clear":    "Unselect the %s",
}

// subUsage overrides verbUsage where a command does more than its verb says.
var subUsage = map[string]string{
	"new project":      "Create a project: config and code repositories on GitHub, cloned here",
	"delete project":   "Delete a project from the cloud and its local clone",
	"query project":    "Show a project (--list for all your projects)",
	"list project":     "List your projects on the selected cloud",
	"select project":   "Select the project to work on (--none to unselect)",
	"clear project":    "Unselect the project and application",
	"clone project":    "Clone a project's config and code repositories",
	"push project":     "Commit and push the config, then the code repository; this starts the builds",
	"pull project":     "Pull the config and code repositories",
	"checkout project": "Switch the config and code repositories to a branch",
	"import project":   "Add a project's existing config and code repositories to the selected cloud",
	"select cloud":     "Choose the cloud: a Dream universe or a remote cloud by FQDN",
	"select app":       "Work inside an application; resource commands then apply to it (--none to leave)",
	"clear app":        "Leave the selected application",
	"query builds":     "List the selected project's build jobs (default: last 7 days)",
	"list builds":      "List the selected project's build jobs (default: last 7 days)",
	"query build":      "Show one build job by job id",
	"cancel build":     "Cancel a build job by job id",
	"retry build":      "Run a build job again by job id",
	"query logs":       "Print or save a build job's logs by job id",
}

var examples = map[string]string{
	"push project": "tau push project -m \"update\" --wait            push, then follow the builds\n" +
		"   tau push project -m \"update\" --wait --serve    and wait until every node serves them\n" +
		"   tau push project --config-only -m \"routes\"     push the config repository only",
	"new project": "tau new project --name shop --description \"my shop\" --private --defaults",
	"new function": "tau new function --name ping --defaults     from the default template, no questions\n" +
		"   tau new function                           asks for each setting",
	"new website":    "tau new website --name site --generate-repository --defaults",
	"select cloud":   "tau select cloud --universe default          a local Dream universe\n   tau select cloud --fqdn cloud.example.com    a remote cloud",
	"import project": "tau import project --config me/shop_config --code me/shop_code",
	"logs":           "tau logs --last code               the last build of the code repository\n   tau logs --last website site -f    follow a website's build\n   tau logs <job-id>",
	"status":         "tau status\n   tau status --watch",
	"doctor":         "tau doctor     run it first whenever something fails",
}

func applyHelp(app *cli.App) {
	app.Name, app.HelpName = "tau", "tau"
	app.Usage = "the Taubyte command line"
	app.Description = gettingStarted
	app.Version = common.VersionLine()
	cli.VersionPrinter = func(ctx *cli.Context) { _ = version.Run(ctx) }
	app.Action = unknownOrHelp
	app.CustomAppHelpTemplate = appHelpTemplate()

	for _, c := range app.Commands {
		c.Category = categories[c.Name]
		if c.Category == "" {
			c.Category = "Cloud" // commands other builds add (ee); the help lists only known categories
		}
		if c.Usage == "" {
			c.Usage = topUsage[c.Name]
		}
		describe(c, c.Name)
		if len(c.Subcommands) > 0 && c.Action == nil {
			c.Action = unknownOrHelp
		}
		for _, s := range c.Subcommands {
			for _, n := range s.Names() {
				if u := subUsage[c.Name+" "+n]; u != "" && s.Usage == "" {
					s.Usage = u
				}
			}
			if s.Usage == "" && verbUsage[c.Name] != "" {
				s.Usage = fmt.Sprintf(verbUsage[c.Name], s.Name)
			}
			describe(s, c.Name+" "+s.Name)
		}
	}
}

// order is how the root help lists categories; urfave would sort them by name.
var order = []string{"Getting started", "Projects & resources", "Build & deploy", "Cloud", "Other"}

func appHelpTemplate() string {
	t := `NAME:
   {{.Name}} - {{.Usage}}

USAGE:
   {{.HelpName}} [global options] command [command options] [arguments...]

{{.Description}}

COMMANDS:`
	for _, cat := range order {
		t += `{{range .VisibleCategories}}{{if eq .Name "` + cat + `"}}
   ` + cat + `:{{range .VisibleCommands}}
     {{join .Names ", "}}{{"	"}}{{.Usage}}{{end}}{{end}}{{end}}`
	}
	// the help command has no category; urfave adds it after this runs
	t += `{{range .VisibleCategories}}{{if not .Name}}{{range .VisibleCommands}}
     {{join .Names ", "}}{{"	"}}{{.Usage}}{{end}}{{end}}{{end}}

GLOBAL OPTIONS:{{template "visibleFlagTemplate" .}}

VERSION:
   {{.Version}}
`
	return t
}

func describe(c *cli.Command, key string) {
	if ex := examples[key]; ex != "" && c.Description == "" {
		c.Description = "Examples:\n   " + ex
	}
}

// unknownOrHelp is the action of tau and of each verb: help, or a suggestion
// for a name that is not one of their commands.
func unknownOrHelp(ctx *cli.Context) error {
	name := ctx.Args().First()
	if name == "" {
		if ctx.Command == nil || ctx.Command.Name == "" || ctx.Command.Name == ctx.App.Name {
			return cli.ShowAppHelp(ctx)
		}
		return cli.ShowSubcommandHelp(ctx)
	}
	cmds, where := ctx.App.Commands, "tau"
	if ctx.Command != nil && ctx.Command.Name != "" && ctx.Command.Name != ctx.App.Name {
		cmds, where = ctx.Command.Subcommands, "tau "+ctx.Command.Name
	}
	msg := fmt.Sprintf("%q is not a `%s` command.", name, where)
	if s := suggest(name, cmds); s != "" {
		msg += fmt.Sprintf(" Did you mean `%s %s`?", where, s)
	}
	return fmt.Errorf("%s\n  hint: `%s --help` lists them", msg, where)
}

// suggest returns the visible command name closest to name, if it is close.
func suggest(name string, cmds []*cli.Command) string {
	name = strings.ToLower(name)
	var names []string
	for _, c := range cmds {
		if !c.Hidden {
			names = append(names, c.Names()...)
		}
	}
	sort.Strings(names)
	best, bestD := "", 3 // three or more edits away is another word
	for _, n := range names {
		d := distance(name, n)
		if len(name) > 1 && strings.HasPrefix(n, name) {
			d = min(d, 1)
		}
		if d < bestD {
			best, bestD = n, d
		}
	}
	return best
}

// distance is the Levenshtein distance between a and b.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
