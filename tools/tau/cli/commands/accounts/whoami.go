package accounts

import (
	"errors"

	"github.com/pterm/pterm"
	accountsClient "github.com/taubyte/tau/tools/tau/clients/accounts_client"
	"github.com/urfave/cli/v2"
)

var whoamiCommand = &cli.Command{
	Name:   "whoami",
	Usage:  "Show the Member identity attached to the current session",
	Action: runWhoami,
}

func runWhoami(_ *cli.Context) error {
	loaded, err := accountsClient.Load()
	if err != nil {
		return err
	}
	if loaded.Profile.AccountsSession == "" {
		return errors.New("no active session — run `tau accounts login` first")
	}

	me, err := loaded.HTTP.Me()
	if err != nil {
		return err
	}
	if me.Member != nil {
		pterm.Info.Printf("Member: %s\n", me.Member.PrimaryEmail)
		pterm.Info.Printf("Role:   %s\n", me.Member.Role)
	}
	for _, acc := range me.Accounts {
		pterm.Info.Printf("Account: %s (%s)\n", acc.Slug, acc.Name)
	}
	if me.Session != nil {
		pterm.Info.Printf("Session expires: %s\n", me.Session.ExpiresAt.Format("2006-01-02 15:04 MST"))
	}
	return nil
}
