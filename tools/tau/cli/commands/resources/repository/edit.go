package repositoryCommands

import (
	"github.com/pterm/pterm"
	repositoryI18n "github.com/taubyte/tau/tools/tau/i18n/repository"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	repositoryLib "github.com/taubyte/tau/tools/tau/lib/repository"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func (lib *repositoryCommands) Edit(ctx *cli.Context) error {
	project, err := projectLib.SelectedProjectConfig()
	if err != nil {
		return err
	}

	resource, err := lib.PromptsGetOrSelect(ctx)
	if err != nil {
		return err
	}

	originalRepoId := resource.Get().RepoID()

	infoIface, err := lib.PromptsEdit(ctx, resource)
	if err != nil {
		return err
	}

	var cloneMethod func() error
	switch info := infoIface.(type) {
	case *repositoryLib.Info:
		if info.DoClone {
			cloneMethod = func() error {
				_, err = info.Clone(project, resource.Get().RepositoryURL(), resource.Get().Branch(), prompts.GetOrAskForEmbedToken(ctx))
				if err != nil {
					return err
				}

				return nil
			}
		}
	}

	confirm := lib.TableConfirm(ctx, resource, lib.PromptsEditThis)
	if confirm {
		if cloneMethod != nil {
			err = cloneMethod()
			if err != nil {
				return err
			}
		}

		// Register the repository if ID has changed
		if originalRepoId != resource.Get().RepoID() {
			err = repositoryLib.Register(resource.Get().RepoID())
			if err != nil {
				// should not fail on registration as it could already be registered
				pterm.Warning.Println(repositoryI18n.RegisteringRepositoryFailed(resource.Get().Name(), err))

			} else {
				lib.I18nRegistered(resource.Get().RepositoryURL())
			}
		}

		err := lib.LibSet(resource)
		if err != nil {
			return err
		}
		lib.I18nEdited(resource.Get().Name())

		return nil
	}

	return nil
}
