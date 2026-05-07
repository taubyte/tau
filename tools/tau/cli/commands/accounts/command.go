// Package accounts wires `tau accounts ...` into the urfave/cli v2 app.
//
// Member-facing surface (v1):
//
//   - login / logout / whoami       — Member-session lifecycle.
//   - list                          — Accounts attached to the session.
//   - plans list [account-slug]     — Plan grants across (or within) Accounts.
//   - members invite/list           — Manage other Members on an Account.
//   - users add/list/remove/grant   — Manage linked git accounts and their plan grants.
//
// Plan and Account creation are deliberately absent — those are
// operator-only and reachable only through the P2P verbs today. An
// operator-side CLI is a separate workstream.
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
		plansCommand,
		membersCommand,
		usersCommand,
	},
}
