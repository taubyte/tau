package cli

import "github.com/spf13/cobra"

func buildCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build [dir]",
		Short: "build the project on this machine, as a cloud would; a development cloud is handed the result",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := flagString(cmd, "dir")
			if len(args) == 1 {
				dir = args[0]
			}
			return runBuild(cmd, env, dir)
		},
	}
	addDir(cmd)
	addEnv(cmd)
	addProject(cmd)
	cmd.Flags().Bool("clean", false, "build what HEAD holds (git archive HEAD) rather than the working tree")
	cmd.Flags().Bool("published", false, "build against the configuration the cloud has published, rather than this directory's")
	cmd.Flags().String("branch", "", "the branch whose deployments are built (default: the checked-out one)")
	cmd.Flags().String("repo", "", "build this code repository's resources, against what the cloud published (default for a directory with no project: its origin)")
	return cmd
}
