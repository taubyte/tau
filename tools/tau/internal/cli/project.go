package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/taubyte/tau/tools/tau/internal/project"
)

func initCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init [name]",
		Short: "create a project: project.yaml and a starter function behind a gateway in a zone",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := flagString(cmd, "dir")
			name := arg(args, 0)
			if name == "" {
				if !prompting(cmd, env) {
					return errors.New("a project name is required: tau init <name>")
				}
				var err error
				if name, err = env.Prompter.Input("Project name", "", nil); err != nil {
					return err
				}
			}
			written, err := project.Init(cmd.Context(), dir, name, flagBool(cmd, "force"))
			if err != nil {
				return err
			}
			t := theme(env)
			var b strings.Builder
			b.WriteString(t.Done(fmt.Sprintf("created project %q in %s", name, dir)))
			for _, w := range written {
				b.WriteString("  " + t.Muted.Render(w) + "\n")
			}
			b.WriteString(t.Muted.Render("next: `tau validate`, then `tau link` it to a cloud") + "\n")
			return emit(cmd, env, map[string]any{"name": name, "dir": dir, "written": written}, b.String())
		},
	}
	addDir(cmd)
	cmd.Flags().Bool("force", false, "overwrite a project already in the directory")
	return cmd
}

func newCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new <kind> <name>",
		Short: "add a resource of any kind the schema declares (run with no kind to list them)",
		RunE: func(cmd *cobra.Command, args []string) error {
			t := theme(env)
			kinds := project.Kinds()
			kind, name := arg(args, 0), arg(args, 1)
			if kind == "" {
				if !prompting(cmd, env) {
					return emit(cmd, env, kinds, t.Kinds(kinds))
				}
				names := make([]string, 0, len(kinds))
				for _, k := range kinds {
					names = append(names, k.Name)
				}
				var err error
				if kind, err = env.Prompter.Select("Kind", names); err != nil {
					return err
				}
			}
			if name == "" {
				if !prompting(cmd, env) {
					return errors.New("a name is required: tau new <kind> <name>")
				}
				var err error
				if name, err = env.Prompter.Input("Name", "", nil); err != nil {
					return err
				}
			}

			values := map[string]string{}
			for _, kv := range flagStrings(cmd, "set") {
				k, v, ok := strings.Cut(kv, "=")
				if !ok || k == "" {
					return fmt.Errorf("--set %q is not path=value", kv)
				}
				values[k] = v
			}

			p, err := project.Open(flagString(cmd, "dir"))
			if err != nil {
				return err
			}
			if !p.IsProject() {
				return errors.New("no project here: run `tau init` first, or pass --dir")
			}
			res, issues, err := p.New(kind, name, flagString(cmd, "in"), values)
			if err != nil {
				return err
			}

			// At a terminal, ask for each field the resource still owes, checked
			// against its declared rule as it is typed.
			for prompting(cmd, env) && len(issues) > 0 {
				issue := issues[0]
				field := strings.Join(issue.Field, ".")
				v, err := env.Prompter.Input(field, issue.Message, func(s string) error { return p.CheckField(res, field, s) })
				if err != nil {
					return err
				}
				if err := p.SetField(res, field, v); err != nil {
					return err
				}
				issues = p.Issues(res)
			}

			if len(issues) > 0 && !flagBool(cmd, "incomplete") {
				var hint []string
				for _, i := range issues {
					hint = append(hint, "--set "+strings.Join(i.Field, ".")+"=…")
				}
				if !jsonOut(cmd) {
					fmt.Fprint(env.Stderr, t.Issues(issues))
				}
				return fmt.Errorf("%s still needs %d field(s): %s (or --incomplete)", strings.Join(res, "/"), len(issues), strings.Join(hint, " "))
			}

			written, err := p.Save()
			if err != nil {
				return err
			}
			var b strings.Builder
			b.WriteString(t.Done("created " + strings.Join(res, "/")))
			for _, w := range written {
				b.WriteString("  " + t.Muted.Render(filepath.ToSlash(w)) + "\n")
			}
			if len(issues) > 0 {
				b.WriteString(t.Issues(issues))
			}
			return emit(cmd, env, map[string]any{"resource": res, "written": written, "issues": issues}, b.String())
		},
	}
	addDir(cmd)
	cmd.Flags().String("in", "", "the scope to create it in, e.g. shop or shop/local")
	cmd.Flags().StringArray("set", nil, "a field value, path=yaml (e.g. execution.timeout=10s); repeatable")
	cmd.Flags().Bool("incomplete", false, "write it even though required fields are missing")
	return cmd
}

func validateCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "compile the project and audit it: Broken fails, Check and Note inform",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := project.Open(flagString(cmd, "dir"))
			if err != nil {
				return err
			}
			if !p.IsProject() {
				return errors.New("no project here: run `tau init` first, or pass --dir")
			}
			r := p.Validate(cmd.Context(), flagString(cmd, "env"))
			if err := emit(cmd, env, r, theme(env).Report(r)); err != nil {
				return err
			}
			if r.Broken() {
				return &exitError{code: ExitBroken}
			}
			return nil
		},
	}
	addDir(cmd)
	addEnv(cmd)
	return cmd
}

// viewsCommand shows the compiled project cut per destination. The cut is the
// schema's (its Viewer): with no regions bound it is one view of everything,
// and as region views land in the schema this command shows them unchanged.
func viewsCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "views",
		Short: "show what each destination is given of the compiled project",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := project.Open(flagString(cmd, "dir"))
			if err != nil {
				return err
			}
			viewed, err := p.Views(cmd.Context(), flagString(cmd, "env"))
			if err != nil {
				return err
			}
			type out struct {
				Views any `json:"views"`
				Notes any `json:"notes"`
			}
			return emit(cmd, env, out{Views: viewed.Views, Notes: viewed.Notes}, theme(env).Views(viewed))
		},
	}
	addDir(cmd)
	addEnv(cmd)
	return cmd
}
