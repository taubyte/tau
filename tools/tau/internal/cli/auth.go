package cli

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/taubyte/tau/tools/tau/internal/config"
)

func validCloud(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q is not a cloud URL (want https://host[:port])", s)
	}
	return nil
}

// loginCommand stores a cloud and a forge token as a profile. The frontend
// authenticates a bearer forge token and asks the forge what its holder may
// write, so the token IS the login: there is no session to open.
func loginCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "store a cloud and the forge token used against it",
		Long: "The cloud authorizes each request by asking the repository's forge what the token's\n" +
			"holder may write, so the token is a forge token: a GitHub personal access token with\n" +
			"repository access, for example. It is stored 0600 in the user config directory.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			name := flagString(cmd, "profile")
			if name == "" {
				name = config.DefaultProfile
			}
			p := cfg.Profiles[name]

			if v := flagString(cmd, "cloud"); v != "" {
				p.Cloud = v
			}
			if p.Cloud == "" {
				if p.Cloud, err = need(cmd, env, "cloud", "Cloud URL (the build frontend)", validCloud); err != nil {
					return err
				}
			}
			if err := validCloud(p.Cloud); err != nil {
				return err
			}

			switch {
			case flagBool(cmd, "token-stdin"):
				line, err := bufio.NewReader(env.Stdin).ReadString('\n')
				if err != nil && line == "" {
					return errors.New("reading the token from stdin: nothing there")
				}
				p.Token = strings.TrimSpace(line)
			case flagString(cmd, "token") != "":
				p.Token = flagString(cmd, "token")
			case prompting(cmd, env):
				if p.Token, err = env.Prompter.Secret("Forge token", "a token with write access to your project's repositories"); err != nil {
					return err
				}
			default:
				return errors.New("--token or --token-stdin is required")
			}
			if strings.TrimSpace(p.Token) == "" {
				return errors.New("the token is empty")
			}

			cfg.Profiles[name] = p
			if flagBool(cmd, "use") || cfg.Current == "" {
				cfg.Current = name
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			return emit(cmd, env, map[string]string{"profile": name, "cloud": p.Cloud, "config": cfg.File()},
				theme(env).Done(fmt.Sprintf("logged in to %s as profile %q (%s)", p.Cloud, name, cfg.File())))
		},
	}
	cmd.Flags().String("token", "", "the forge token (prefer --token-stdin: a flag lands in shell history)")
	cmd.Flags().Bool("token-stdin", false, "read the token from stdin")
	cmd.Flags().Bool("use", true, "make this the current profile")
	return cmd
}

func logoutCommand(env Env) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "forget a profile's token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			name := flagString(cmd, "profile")
			if name == "" {
				name = cfg.Current
			}
			p, ok := cfg.Profiles[name]
			if !ok {
				return fmt.Errorf("no profile %q", name)
			}
			p.Token = ""
			cfg.Profiles[name] = p
			if err := cfg.Save(); err != nil {
				return err
			}
			return emit(cmd, env, map[string]string{"profile": name}, theme(env).Done(fmt.Sprintf("forgot the token of %q", name)))
		},
	}
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
