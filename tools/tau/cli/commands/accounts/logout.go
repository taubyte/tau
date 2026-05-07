package accounts

import (
	"github.com/pterm/pterm"
	accountsClient "github.com/taubyte/tau/tools/tau/clients/accounts_client"
	"github.com/urfave/cli/v2"
)

var logoutCommand = &cli.Command{
	Name:   "logout",
	Usage:  "Revoke the current Account session and clear it from your tau profile",
	Action: runLogout,
}

func runLogout(_ *cli.Context) error {
	loaded, err := accountsClient.Load()
	if err != nil {
		return err
	}
	if loaded.Profile.AccountsSession == "" {
		pterm.Info.Println("No active session — nothing to log out from.")
		return nil
	}

	// Best-effort server revoke. If the server is unreachable or the token
	// is already revoked, still clear it locally.
	if err := loaded.HTTP.Logout(); err != nil {
		pterm.Warning.Printf("Server logout failed (clearing local session anyway): %v\n", err)
	}

	if err := accountsClient.PersistSession(loaded.ProfileName, loaded.Profile, ""); err != nil {
		return err
	}
	pterm.Success.Println("Signed out.")
	return nil
}
