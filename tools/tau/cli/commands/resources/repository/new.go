package repositoryCommands

import (
	"fmt"
	"path"

	"github.com/pterm/pterm"
	repositoryI18n "github.com/taubyte/tau/tools/tau/i18n/repository"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	repositoryLib "github.com/taubyte/tau/tools/tau/lib/repository"
	"github.com/taubyte/tau/tools/tau/prompts"
	authClient "github.com/taubyte/tau/tools/tau/singletons/auth_client"
	"github.com/taubyte/tau/tools/tau/singletons/config"
	"github.com/urfave/cli/v2"
)

func (lib *repositoryCommands) New(ctx *cli.Context) error {
	project, err := projectLib.SelectedProjectConfig()
	if err != nil {
		return err
	}

	infoIface, resource, err := lib.PromptNew(ctx)
	if err != nil {
		return err
	}

	var (
		doRegistration bool
		cloneMethod    func() error
	)
	switch info := infoIface.(type) {
	case *repositoryLib.InfoTemplate:
		doRegistration = true
		cloneMethod, err = handleTemplateClone(ctx, project, resource, lib, info)
		if err != nil {
			return err
		}

	case *repositoryLib.Info:
		if info.DoClone {
			cloneMethod = func() error {
				_, err := info.Clone(project, resource.Get().RepositoryURL(), resource.Get().Branch(), prompts.GetOrAskForEmbedToken(ctx))
				if err != nil {
					return err
				}

				return nil
			}
		}

	default:
		return fmt.Errorf("unknown return type: %T", info)
	}

	if lib.TableConfirm(ctx, resource, lib.PromptsCreateThis) {
		if cloneMethod != nil {
			err = cloneMethod()
			if err != nil {
				return err
			}
		}

		// Register the repository
		if doRegistration {
			err = repositoryLib.Register(resource.Get().RepoID())
			if err != nil {
				// should not fail on registration as it could already be registered
				pterm.Warning.Println(repositoryI18n.RegisteringRepositoryFailed(resource.Get().Name(), err))
			} else {
				lib.I18nRegistered(resource.Get().RepositoryURL())
			}
		}

		err = lib.LibNew(resource)
		if err != nil {
			return err
		}
		lib.I18nCreated(resource.Get().Name())

		return nil
	}

	return nil
}

func handleTemplateClone(ctx *cli.Context, project config.Project, resource Resource, lib *repositoryCommands, info *repositoryLib.InfoTemplate) (cloneMethod func() error, err error) {
	client, err := authClient.Load()
	if err != nil {
		return
	}

	profile, err := loginLib.GetSelectedProfile()
	if err != nil {
		return
	}

	_repoId, err := client.CreateRepository(info.RepositoryName, resource.Get().Description(), info.Private)
	if err != nil {
		return
	}
	resource.Set().RepoID(_repoId)
	resource.Set().RepoName(path.Join(profile.GitUsername, info.RepositoryName))

	cloneMethod = func() error {
		repository, err := (&repositoryLib.Info{
			FullName: resource.Get().RepoName(),
			ID:       _repoId,
			Type:     lib.Type,
			DoClone:  true,
		}).Clone(project, resource.Get().RepositoryURL(), resource.Get().Branch(), prompts.GetOrAskForEmbedToken(ctx))
		if err != nil {
			return err
		}

		err = info.Info.CloneTo(repository.Root())
		if err != nil {
			return err
		}

		err = repository.Commit("init", ".")
		if err != nil {
			return err
		}

		err = repository.Push()
		if err != nil {
			return err
		}

		return nil
	}

	return
}
