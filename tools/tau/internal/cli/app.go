// Package cli is the tau command line: cobra commands over the build
// frontend's API (internal/api) and the schema session (internal/project),
// drawn by internal/ui.
//
// EVERY COMMAND RUNS WITHOUT A TERMINAL. A value a command would ask for is
// also a flag; it asks only when stdin and stdout are both a terminal, and
// otherwise fails naming the flag. --output json prints the data and nothing
// else, and --yes stands in for every confirmation.
//
// Cobra rather than urfave/cli (which the node binary uses) because a developer
// CLI needs flags after arguments (`tau new function hello --set …`) and flags
// shared by every command (`-o json`); urfave/cli v2 stops parsing flags at the
// first argument and has no persistent flags.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/taubyte/tau/tools/tau/internal/api"
	"github.com/taubyte/tau/tools/tau/internal/config"
	"github.com/taubyte/tau/tools/tau/internal/ui"
)

// Env is everything a command touches outside itself, so a test can hand it a
// buffer, a fake prompter and a program runner.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	// Interactive is whether the developer is at a terminal; only then does a
	// command prompt or draw a live view.
	Interactive bool
	Prompter    ui.Prompter
	HTTP        *http.Client
	// Program runs a bubbletea model to completion; alt is whether it takes
	// the whole screen.
	Program func(m tea.Model, alt bool) (tea.Model, error)
}

// System is the Env of a real process.
func System() Env {
	interactive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	return Env{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		Interactive: interactive,
		Prompter:    ui.Huh{Accessible: os.Getenv("TAU_ACCESSIBLE") == "1"},
		HTTP:        http.DefaultClient,
		Program: func(m tea.Model, alt bool) (tea.Model, error) {
			opts := []tea.ProgramOption{tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout)}
			if alt {
				opts = append(opts, tea.WithAltScreen())
			}
			return tea.NewProgram(m, opts...).Run()
		},
	}
}

// ExitBroken is the exit code of a project that does not compile or audits
// Broken; any other failure is 1.
const ExitBroken = 2

// exitError carries an exit code past cobra, with or without a message.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

// New builds the root command over env.
func New(env Env) *cobra.Command {
	root := &cobra.Command{
		Use:           "tau",
		Short:         "build, validate and deploy tau projects",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if o := flagString(cmd, "output"); o != "text" && o != "json" {
				return fmt.Errorf("--output is text or json, not %q", o)
			}
			return nil
		},
	}
	root.SetIn(env.Stdin)
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	pf := root.PersistentFlags()
	pf.String("profile", os.Getenv("TAU_PROFILE"), "the profile to use (see `tau profiles`)")
	pf.String("cloud", "", "the cloud's build frontend URL, overriding the profile's")
	pf.StringP("output", "o", "text", "text or json")
	pf.BoolP("yes", "y", false, "assume yes to every confirmation")

	root.AddCommand(
		loginCommand(env), logoutCommand(env), profilesCommand(env),
		initCommand(env), newCommand(env), validateCommand(env), viewsCommand(env),
		linkCommand(env), deployCommand(env), statusCommand(env),
		jobsCommand(env), jobCommand(env), logsCommand(env), cancelCommand(env), retryCommand(env),
		secretsCommand(env),
	)
	return root
}

// Run runs args (without the program name) and returns the exit code, writing
// any error to stderr.
func Run(env Env, args []string) int {
	root := New(env)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	if err == nil {
		return 0
	}
	code := 1
	var exit *exitError
	if errors.As(err, &exit) {
		code = exit.code
	}
	if msg := err.Error(); msg != "" {
		fmt.Fprintln(env.Stderr, ui.NewTheme(env.Stderr).Bad.Render("error:")+" "+msg)
	}
	return code
}

// ── flags ────────────────────────────────────────────────────────────────────

func addDir(cmd *cobra.Command) {
	cmd.Flags().StringP("dir", "C", ".", "the project directory")
}

func addProject(cmd *cobra.Command) {
	cmd.Flags().String("project", os.Getenv("TAU_PROJECT"), "the cloud project id, overriding the directory's link")
}

func addEnv(cmd *cobra.Command) {
	cmd.Flags().StringP("env", "e", "", "the environment (deployment) to use")
}

// The flag readers. Every flag read is one the command declares, so a lookup
// error is a programming mistake the tests catch, not a runtime condition.
func flagString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

func flagBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name)
	return v
}

func flagInt(cmd *cobra.Command, name string) int {
	v, _ := cmd.Flags().GetInt(name)
	return v
}

func flagInt64(cmd *cobra.Command, name string) int64 {
	v, _ := cmd.Flags().GetInt64(name)
	return v
}

func flagDuration(cmd *cobra.Command, name string) time.Duration {
	v, _ := cmd.Flags().GetDuration(name)
	return v
}

func flagStrings(cmd *cobra.Command, name string) []string {
	v, _ := cmd.Flags().GetStringArray(name)
	return v
}

// ── shared helpers ───────────────────────────────────────────────────────────

func jsonOut(cmd *cobra.Command) bool { return flagString(cmd, "output") == "json" }

// emit prints v as json under --output json, and text otherwise.
func emit(cmd *cobra.Command, env Env, v any, text string) error {
	if jsonOut(cmd) {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	_, err := io.WriteString(env.Stdout, text)
	return err
}

func theme(env Env) ui.Theme { return ui.NewTheme(env.Stdout) }

// prompting reports whether a command may ask: a terminal, and not --output json.
func prompting(cmd *cobra.Command, env Env) bool {
	return env.Interactive && env.Prompter != nil && !jsonOut(cmd)
}

// need is a value from a flag, or asked for at a terminal, or an error naming
// the flag.
func need(cmd *cobra.Command, env Env, flag, title string, validate func(string) error) (string, error) {
	if v := flagString(cmd, flag); v != "" {
		return v, nil
	}
	if !prompting(cmd, env) {
		return "", fmt.Errorf("--%s is required", flag)
	}
	return env.Prompter.Input(title, "", validate)
}

// confirm is --yes, or a yes at a terminal. Without either it refuses.
func confirm(cmd *cobra.Command, env Env, question string) error {
	if flagBool(cmd, "yes") {
		return nil
	}
	if !prompting(cmd, env) {
		return errors.New("refusing without confirmation: pass --yes")
	}
	ok, err := env.Prompter.Confirm(question)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("not confirmed")
	}
	return nil
}

func loadConfig() (*config.Config, error) {
	path, err := config.Path()
	if err != nil {
		return nil, err
	}
	return config.Load(path)
}

// client is the API client for the selected profile.
func client(cmd *cobra.Command, env Env) (*api.Client, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	_, p, err := cfg.Resolve(flagString(cmd, "profile"), flagString(cmd, "cloud"))
	if err != nil {
		return nil, err
	}
	return api.New(p.Cloud, p.Token, env.HTTP)
}

// projectID is --project, or the directory's link.
func projectID(cmd *cobra.Command) (string, error) {
	if id := flagString(cmd, "project"); id != "" {
		return id, nil
	}
	l, err := config.ReadLink(flagString(cmd, "dir"))
	if err != nil {
		return "", err
	}
	return l.ProjectID, nil
}

func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}
