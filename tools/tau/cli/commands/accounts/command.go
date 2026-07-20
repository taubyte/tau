// Package accounts wires `tau accounts ...` into the urfave/cli v2 app.
//
// Member-facing surface (v1):
//
//   - login / logout / whoami       — Member-session lifecycle.
//   - list                          — Accounts attached to the session.
//   - members invite/list           — Manage other Members on an Account.
//   - users add/list/remove         — Manage linked git accounts.
//
// Some builds attach further subcommands via a build seam. Account creation is
// an operator-only P2P verb.
package accounts

import "github.com/urfave/cli/v2"

// Command is the top-level "accounts" command tree. Registered from
// tools/tau/cli/new.go.
var Command = &cli.Command{
	Name:    "accounts",
	Aliases: []string{"acc"},
	Usage:   "Manage your tau Account session and team",
	Subcommands: []*cli.Command{
		loginCommand,
		logoutCommand,
		whoamiCommand,
		listCommand,
		membersCommand,
		usersCommand,
	},
}
