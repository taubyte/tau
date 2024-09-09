package project

import (
	"os"
	"path"

	"github.com/taubyte/tau/pkg/cli/i18n"
	"github.com/taubyte/tau/pkg/git"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	projectFlags "github.com/taubyte/tau/tools/tau/flags/project"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/taubyte/tau/tools/tau/prompts"
	projectPrompts "github.com/taubyte/tau/tools/tau/prompts/project"
	"github.com/taubyte/tau/tools/tau/singletons/config"
	"github.com/urfave/cli/v2"
)

func (link) Clone() common.Command {
	return common.Create(
		&cli.Command{
			Flags: flags.Combine(
				flags.Yes,
				projectFlags.Loc,
				flags.Branch,
				flags.EmbedToken,
				flags.Select,
			),
			Action: clone,
		},
	)
}

func clone(c *cli.Context) error {
	checkEnv := !c.Bool(flags.Select.Name)

	// TODO should select offer projects that are already cloned?
	project, err := projectPrompts.GetOrSelect(c, checkEnv)
	if err != nil {
		return err
	}

	configProject := config.Project{
		Name: project.Name,
	}

	// Check location flag, otherwise clone into cwd
	if c.IsSet(projectFlags.Loc.Name) {
		configProject.Location = c.String(projectFlags.Loc.Name)
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return i18n.GettingCwdFailed(err)
		}

		configProject.Location = path.Join(cwd, project.Name)
	}

	repository, err := projectLib.Repository(project.Name).Clone(configProject, prompts.GetOrAskForEmbedToken(c))
	if err != nil {
		return projectI18n.CloningProjectFailed(project.Name, err)
	}

	config, err := repository.Config()
	if err != nil {
		return err
	}

	branch, err := prompts.SelectABranch(c, config)
	if err != nil {
		return err
	}

	currentBranch, err := repository.CurrentBranch()
	if err != nil {
		return err
	}
	if branch != currentBranch {
		return (&dualRepoHandler{
			ctx:         c,
			repository:  repository,
			projectName: project.Name,
			errorFormat: projectI18n.CheckingOutProjectFailed,
			action: func(r *git.Repository) error {
				return r.Checkout(branch)
			},
		}).Run()
	}

	return nil
}
