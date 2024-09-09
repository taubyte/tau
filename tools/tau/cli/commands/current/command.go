package current

import (
	"github.com/taubyte/tau/tools/tau/env"
	"github.com/taubyte/tau/tools/tau/prompts"
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
	selectedProfile, _ := env.GetSelectedUser()
	selectedProject, _ := env.GetSelectedProject()
	selectedApplication, _ := env.GetSelectedApplication()
	selectedNetwork, _ := env.GetSelectedNetwork()
	customNetworkUrl, _ := env.GetCustomNetworkUrl()

	toRender := [][]string{
		{"Profile", parseIfEmpty(selectedProfile)},
		{"Project", parseIfEmpty(selectedProject)},
		{"Application", parseIfEmpty(selectedApplication)},
		{"Network Type", parseIfEmpty(selectedNetwork)},
		{"Network", parseIfEmpty(customNetworkUrl)},
	}

	prompts.RenderTableWithMerge(toRender)
	return nil
}
