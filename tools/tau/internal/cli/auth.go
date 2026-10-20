package cli

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

func validCloud(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q is not a cloud URL (want https://host[:port])", s)
	}
	return nil
}

type profileRow struct {
	Name     string `json:"name"`
	Cloud    string `json:"cloud"`
	Current  bool   `json:"current"`
	LoggedIn bool   `json:"logged_in"`
}

func profilesCommand(env Env) *cobra.Command {
	return &cobra.Command{
		Use:   "profiles [name]",
		Short: "list profiles, or switch to one",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if name := arg(args, 0); name != "" {
				if _, ok := cfg.Profiles[name]; !ok {
					return fmt.Errorf("no profile %q", name)
				}
				cfg.Current = name
				if err := cfg.Save(); err != nil {
					return err
				}
				return emit(cmd, env, map[string]string{"current": name}, theme(env).Done("using "+name))
			}
			t := theme(env)
			rows := []profileRow{}
			var b strings.Builder
			for _, name := range cfg.Names() {
				p := cfg.Profiles[name]
				row := profileRow{Name: name, Cloud: p.Cloud, Current: name == cfg.Current, LoggedIn: p.Token != ""}
				rows = append(rows, row)
				mark := "  "
				if row.Current {
					mark = t.OK.Render("* ")
				}
				state := ""
				if !row.LoggedIn {
					state = t.Muted.Render(" (logged out)")
				}
				fmt.Fprintf(&b, "%s%s %s%s\n", mark, t.Title.Render(name), p.Cloud, state)
			}
			if len(rows) == 0 {
				b.WriteString(t.Muted.Render("no profiles: run `tau login --cloud <url>`") + "\n")
			}
			return emit(cmd, env, rows, b.String())
		},
	}
}
