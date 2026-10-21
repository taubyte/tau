package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/taubyte/tau/tools/tau/internal/api"
)

// grantsCommand is an owner sharing a project with other tau users: viewer
// reads jobs and logs, builder also cancels and retries, secret-manager also
// manages secrets, owner also grants and relinks.
func grantsCommand(env Env) *cobra.Command {
	scoped := func(c *cobra.Command) *cobra.Command {
		addDir(c)
		addProject(c)
		return c
	}

	list := scoped(&cobra.Command{
		Use:   "list",
		Short: "list who holds a role on the project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id, err := projectID(cmd)
			if err != nil {
				return err
			}
			cl, err := client(cmd, env)
			if err != nil {
				return err
			}
			gs, err := cl.Grants(cmd.Context(), id)
			if err != nil {
				return err
			}
			if gs == nil {
				gs = []api.Grant{}
			}
			t := theme(env)
			var b strings.Builder
			if len(gs) == 0 {
				b.WriteString(t.Muted.Render("nobody else holds a role on "+id) + "\n")
			}
			for _, g := range gs {
				fmt.Fprintf(&b, "%s  %s %s  %s\n", t.Accent.Render(g.ID), g.User, t.Title.Render(g.Role), t.Muted.Render("by "+g.By+" "+g.At.UTC().Format(time.RFC3339)))
			}
			return emit(cmd, env, gs, b.String())
		},
	})

	add := scoped(&cobra.Command{
		Use:   "add <user-id> <role>",
		Short: "give a tau user a role on the project: viewer, builder, secret-manager or owner",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := projectID(cmd)
			if err != nil {
				return err
			}
			cl, err := client(cmd, env)
			if err != nil {
				return err
			}
			gid, err := cl.GrantRole(cmd.Context(), id, args[0], args[1])
			if err != nil {
				return err
			}
			return emit(cmd, env, map[string]string{"id": gid, "user": args[0], "role": args[1]},
				theme(env).Done(fmt.Sprintf("%s is %s on %s (grant %s)", args[0], args[1], id, gid)))
		},
	})

	revoke := scoped(&cobra.Command{
		Use:   "revoke <grant-id>",
		Short: "withdraw a grant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := projectID(cmd)
			if err != nil {
				return err
			}
			if err := confirm(cmd, env, "Revoke grant "+args[0]+" on "+id+"?"); err != nil {
				return err
			}
			cl, err := client(cmd, env)
			if err != nil {
				return err
			}
			if err := cl.Revoke(cmd.Context(), id, args[0]); err != nil {
				return err
			}
			return emit(cmd, env, map[string]string{"revoked": args[0]}, theme(env).Done("revoked "+args[0]))
		},
	})

	cmd := &cobra.Command{Use: "grants", Short: "share a project: who holds which role on it (owners only)"}
	cmd.AddCommand(list, add, revoke)
	return cmd
}
