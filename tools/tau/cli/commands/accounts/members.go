package accounts

import (
	"errors"

	"github.com/pterm/pterm"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/urfave/cli/v2"
)

var membersCommand = &cli.Command{
	Name:    "members",
	Aliases: []string{"member"},
	Usage:   "Invite and list Members on an Account",
	Subcommands: []*cli.Command{
		membersInviteCommand,
		membersListCommand,
	},
}

var membersInviteCommand = &cli.Command{
	Name:      "invite",
	Usage:     "Invite a new Member to an Account",
	ArgsUsage: "<account-slug>",
	Flags: []cli.Flag{
		&cli.StringFlag{Name: "email", Usage: "Member's primary email", Required: true},
		&cli.StringFlag{Name: "role", Usage: "owner | admin | viewer | billing", Value: "admin"},
	},
	Action: runMembersInvite,
}

func runMembersInvite(ctx *cli.Context) error {
	loaded, err := requireLoggedIn()
	if err != nil {
		return err
	}
	accountID, err := loaded.resolveAccountID(ctx.Args().First())
	if err != nil {
		return err
	}
	role := accountsIface.Role(ctx.String("role"))
	if !validRole(role) {
		return errors.New("invalid --role; want one of: owner, admin, viewer, billing")
	}
	m, err := loaded.HTTP.InviteMember(accountID, ctx.String("email"), role)
	if err != nil {
		return err
	}
	pterm.Success.Printf("Invited %s as %s — invitation magic-link sent (id: %s)\n", m.PrimaryEmail, m.Role, m.ID)
	return nil
}

var membersListCommand = &cli.Command{
	Name:      "list",
	Usage:     "List Members on an Account",
	ArgsUsage: "<account-slug>",
	Action:    runMembersList,
}

func runMembersList(ctx *cli.Context) error {
	loaded, err := requireLoggedIn()
	if err != nil {
		return err
	}
	accountID, err := loaded.resolveAccountID(ctx.Args().First())
	if err != nil {
		return err
	}
	ids, err := loaded.HTTP.ListMembers(accountID)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		pterm.Info.Println("No Members on this Account.")
		return nil
	}
	// One round-trip per ID; fine for v1's small Account membership. If
	// lists grow past tens, add a server-side `list-with-fields` action.
	for _, id := range ids {
		m, err := loaded.HTTP.GetMember(accountID, id)
		if err != nil {
			pterm.Warning.Printf("%s — error fetching: %s\n", id, err)
			continue
		}
		pterm.Info.Printf("%s — %s (%s)\n", m.PrimaryEmail, m.Role, m.ID)
	}
	return nil
}

func validRole(r accountsIface.Role) bool {
	switch r {
	case accountsIface.RoleOwner, accountsIface.RoleAdmin, accountsIface.RoleViewer, accountsIface.RoleBilling:
		return true
	}
	return false
}
