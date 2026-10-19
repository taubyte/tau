package cli

import (
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/taubyte/tau/tools/tau/internal/project"
)

// environment is --env, or at a terminal one of the environments the project
// declares (asked for by name if it declares none).
func environment(cmd *cobra.Command, env Env) (string, error) {
	if e := flagString(cmd, "env"); e != "" {
		return e, nil
	}
	if !prompting(cmd, env) {
		return "", errors.New("--env is required")
	}
	if p, err := project.Open(flagString(cmd, "dir")); err == nil {
		if envs := p.Environments(); len(envs) > 0 {
			return env.Prompter.Select("Environment", envs)
		}
	}
	return env.Prompter.Input("Environment", "", nil)
}

func secretTarget(cmd *cobra.Command, env Env) (id, environmentName string, err error) {
	if id, err = projectID(cmd); err != nil {
		return "", "", err
	}
	if environmentName, err = environment(cmd, env); err != nil {
		return "", "", err
	}
	return id, environmentName, nil
}

func secretName(args []string) (string, error) {
	if name := arg(args, 0); name != "" {
		return name, nil
	}
	return "", errors.New("a secret name is required")
}

func secretsCommand(env Env) *cobra.Command {
	scoped := func(c *cobra.Command) *cobra.Command {
		addDir(c)
		addProject(c)
		addEnv(c)
		return c
	}

	list := scoped(&cobra.Command{
		Use:   "list",
		Short: "list the secret names set in an environment",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id, e, err := secretTarget(cmd, env)
			if err != nil {
				return err
			}
			cl, err := client(cmd, env)
			if err != nil {
				return err
			}
			names, err := cl.Secrets(cmd.Context(), id, e)
			if err != nil {
				return err
			}
			return emit(cmd, env, names, theme(env).Secrets(e, names))
		},
	})

	set := scoped(&cobra.Command{
		Use:   "set <name> [value]",
		Short: "set a secret (the value from the argument, --value-stdin, or a hidden prompt)",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := secretName(args)
			if err != nil {
				return err
			}
			id, e, err := secretTarget(cmd, env)
			if err != nil {
				return err
			}
			var value []byte
			switch {
			case flagBool(cmd, "value-stdin"):
				if value, err = io.ReadAll(io.LimitReader(env.Stdin, 64<<10+1)); err != nil {
					return err
				}
			case len(args) > 1:
				value = []byte(args[1])
			case prompting(cmd, env):
				v, err := env.Prompter.Secret("Value of "+name, "")
				if err != nil {
					return err
				}
				value = []byte(v)
			default:
				return errors.New("a value is required: an argument, or --value-stdin")
			}
			cl, err := client(cmd, env)
			if err != nil {
				return err
			}
			if err := cl.SetSecret(cmd.Context(), id, e, name, value); err != nil {
				return err
			}
			return emit(cmd, env, map[string]string{"set": name, "env": e}, theme(env).Done("set "+name+" in "+e))
		},
	})
	set.Flags().Bool("value-stdin", false, "read the value from stdin, verbatim")

	unset := scoped(&cobra.Command{
		Use:   "unset <name>",
		Short: "remove a secret",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := secretName(args)
			if err != nil {
				return err
			}
			id, e, err := secretTarget(cmd, env)
			if err != nil {
				return err
			}
			if err := confirm(cmd, env, "Remove "+name+" from "+e+"?"); err != nil {
				return err
			}
			cl, err := client(cmd, env)
			if err != nil {
				return err
			}
			if err := cl.UnsetSecret(cmd.Context(), id, e, name); err != nil {
				return err
			}
			return emit(cmd, env, map[string]string{"unset": name, "env": e}, theme(env).Done("removed "+name+" from "+e))
		},
	})

	root := &cobra.Command{
		Use:   "secrets",
		Short: "the project's secrets: set and list names; values are never read back",
	}
	root.AddCommand(list, set, unset)
	return root
}
