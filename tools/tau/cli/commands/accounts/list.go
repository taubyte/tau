package accounts

import (
	"github.com/pterm/pterm"
	"github.com/urfave/cli/v2"
)

var listCommand = &cli.Command{
	Name:   "list",
	Usage:  "List the Accounts attached to the current session",
	Action: runList,
}

func runList(_ *cli.Context) error {
	loaded, err := requireLoggedIn()
	if err != nil {
		return err
	}
	if len(loaded.Me.Accounts) == 0 {
		pterm.Info.Println("No Accounts linked to this Member.")
		return nil
	}
	for _, acc := range loaded.Me.Accounts {
		// CLI surface treats PRefs as "plans" (the user-facing concept).
		var defaultPlan string
		for _, p := range acc.PRefs {
			if p.IsDefault {
				defaultPlan = p.Name
				break
			}
		}
		if defaultPlan == "" && len(acc.PRefs) > 0 {
			defaultPlan = acc.PRefs[0].Name
		}
		if defaultPlan == "" {
			defaultPlan = "<none>"
		}
		pterm.Info.Printf("%s — %s  (plans: %d, default: %s)\n",
			acc.Slug, acc.Name, len(acc.PRefs), defaultPlan)
	}
	return nil
}
