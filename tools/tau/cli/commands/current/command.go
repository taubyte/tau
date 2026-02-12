package current

import (
	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/urfave/cli/v2"
)

var Command = &cli.Command{
	Name:    "current",
	Usage:   "Display current selected values",
	Aliases: []string{"cur", "here", "this"},
	Action:  Run,
}

func parseIfEmpty(v string) string {
	if len(v) == 0 {
		return "(none)"
	}

	return v
}

func Run(c *cli.Context) error {
	selectedProfile, _ := config.GetSelectedUser()
	selectedProject, _ := config.GetSelectedProject()
	selectedApplication, _ := config.GetSelectedApplication()
	selectedCloud, _ := session.GetSelectedCloud()
	cloudValue, _ := session.GetCustomCloudUrl()

	toRender := [][]string{
		{"Profile", parseIfEmpty(selectedProfile)},
		{"Project", parseIfEmpty(selectedProject)},
		{"Application", parseIfEmpty(selectedApplication)},
		{"Cloud Type", parseIfEmpty(selectedCloud)},
		{"Cloud", parseIfEmpty(cloudValue)},
	}

	prompts.RenderTableWithMerge(toRender)
	return nil
}
