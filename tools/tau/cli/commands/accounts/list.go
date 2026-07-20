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
		pterm.Info.Printf("%s — %s\n", acc.Slug, acc.Name)
	}
	return nil
}
