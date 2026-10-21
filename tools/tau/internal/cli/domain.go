package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// domainCommand prints the TXT record that proves a custom domain is the
// project's. A deploy declaring a zone is refused until it is set.
func domainCommand(env Env) *cobra.Command {
	claim := &cobra.Command{
		Use:   "claim <fqdn>",
		Short: "print the TXT record to set so the cloud serves fqdn for the project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fqdn := strings.TrimSuffix(strings.ToLower(arg(args, 0)), ".")
			if fqdn == "" {
				return errors.New("a domain is required")
			}
			id, err := projectID(cmd)
			if err != nil {
				return err
			}
			cl, err := client(cmd, env)
			if err != nil {
				return err
			}
			c, err := cl.ClaimDomain(cmd.Context(), id, fqdn)
			if err != nil {
				return err
			}
			t := theme(env)
			text := fmt.Sprintf("%s\n  %s  %s  %s\n", t.Title.Render("set this record at "+fqdn+"'s DNS"),
				t.Accent.Render(c.Entry), strings.ToUpper(c.Type), c.Token)
			return emit(cmd, env, c, text)
		},
	}
	addDir(claim)
	addProject(claim)

	root := &cobra.Command{
		Use:   "domain",
		Short: "prove a custom domain is the project's",
	}
	root.AddCommand(claim)
	return root
}
