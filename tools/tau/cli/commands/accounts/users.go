package accounts

import (
	"github.com/pterm/pterm"
	"github.com/urfave/cli/v2"
)

var usersCommand = &cli.Command{
	Name:    "users",
	Aliases: []string{"user"},
	Usage:   "Manage linked git accounts (Users) on an Account",
	Subcommands: []*cli.Command{
		usersAddCommand,
		usersListCommand,
		usersRemoveCommand,
	},
}

// AddUsersSubcommand appends a subcommand under `accounts users`. Build seams
// use it to attach subcommands that only exist in some builds.
func AddUsersSubcommand(cmd *cli.Command) {
	usersCommand.Subcommands = append(usersCommand.Subcommands, cmd)
}

var usersAddCommand = &cli.Command{
	Name:      "add",
	Usage:     "Link a git provider account to an Account",
	ArgsUsage: "<account-slug>",
	Flags: []cli.Flag{
		&cli.StringFlag{Name: "provider", Usage: "git provider (e.g. github)", Value: "github"},
		&cli.StringFlag{Name: "external-id", Usage: "git provider account ID", Required: true},
		&cli.StringFlag{Name: "display", Usage: "display name (e.g. github username)"},
	},
	Action: runUsersAdd,
}

func runUsersAdd(ctx *cli.Context) error {
	loaded, err := requireLoggedIn()
	if err != nil {
		return err
	}
	accountID, err := loaded.resolveAccountID(ctx.Args().First())
	if err != nil {
		return err
	}
	u, err := loaded.HTTP.AddUser(accountID, ctx.String("provider"), ctx.String("external-id"), ctx.String("display"))
	if err != nil {
		return err
	}
	pterm.Success.Printf("Linked %s:%s (%s) to Account — user id: %s\n", u.Provider, u.ExternalID, u.DisplayName, u.ID)
	return nil
}

var usersListCommand = &cli.Command{
	Name:      "list",
	Usage:     "List Users on an Account",
	ArgsUsage: "<account-slug>",
	Action:    runUsersList,
}

func runUsersList(ctx *cli.Context) error {
	loaded, err := requireLoggedIn()
	if err != nil {
		return err
	}
	accountID, err := loaded.resolveAccountID(ctx.Args().First())
	if err != nil {
		return err
	}
	ids, err := loaded.HTTP.ListUsers(accountID)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		pterm.Info.Println("No Users on this Account.")
		return nil
	}
	// IDs only in v1 — a full record listing would balloon the output.
	for _, id := range ids {
		pterm.Info.Println(id)
	}
	return nil
}

var usersRemoveCommand = &cli.Command{
	Name:      "remove",
	Aliases:   []string{"rm"},
	Usage:     "Unlink a git account from an Account",
	ArgsUsage: "<account-slug> <user-id>",
	Action:    runUsersRemove,
}

func runUsersRemove(ctx *cli.Context) error {
	loaded, err := requireLoggedIn()
	if err != nil {
		return err
	}
	if ctx.NArg() != 2 {
		return cli.ShowSubcommandHelp(ctx)
	}
	accountID, err := loaded.resolveAccountID(ctx.Args().Get(0))
	if err != nil {
		return err
	}
	userID := ctx.Args().Get(1)
	if err := loaded.HTTP.RemoveUser(accountID, userID); err != nil {
		return err
	}
	pterm.Success.Printf("Removed user %s\n", userID)
	return nil
}
