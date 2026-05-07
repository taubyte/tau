package accounts

import (
	"github.com/pterm/pterm"
	"github.com/urfave/cli/v2"
)

// Plans are operator-managed in v1; Members can only inspect via list.
var plansCommand = &cli.Command{
	Name:    "plans",
	Aliases: []string{"plan"},
	Usage:   "Inspect Plans within an Account",
	Subcommands: []*cli.Command{
		plansListCommand,
	},
}

var plansListCommand = &cli.Command{
	Name:        "list",
	Usage:       "List plans across the current Member's Accounts",
	ArgsUsage:   "[account-slug]",
	Description: "Without an arg, lists plans across every Account the Member belongs to. Pass an Account slug to filter.",
	Action:      runPlansList,
}

func runPlansList(ctx *cli.Context) error {
	loaded, err := requireLoggedIn()
	if err != nil {
		return err
	}
	filter := ctx.Args().First()
	any := false
	for _, acc := range loaded.Me.Accounts {
		if filter != "" && acc.Slug != filter {
			continue
		}
		if len(acc.Plans) == 0 {
			pterm.Info.Printf("%s: <no plans>\n", acc.Slug)
			any = true
			continue
		}
		for _, p := range acc.Plans {
			marker := " "
			if p.IsDefault {
				marker = "*"
			}
			pterm.Info.Printf("%s %s/%s\n", marker, acc.Slug, p.Slug)
			any = true
		}
	}
	if !any {
		if filter != "" {
			pterm.Info.Printf("Account %q not found among the Member's linked Accounts.\n", filter)
		} else {
			pterm.Info.Println("No plans across any Account.")
		}
	}
	return nil
}
