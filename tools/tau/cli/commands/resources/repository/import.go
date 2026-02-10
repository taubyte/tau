package repositoryCommands

import (
	authClient "github.com/taubyte/tau/tools/tau/clients/auth_client"
	repositoryI18n "github.com/taubyte/tau/tools/tau/i18n/repository"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/urfave/cli/v2"
)

func (lib *repositoryCommands) Import(ctx *cli.Context) error {
	profile, err := loginLib.GetSelectedProfile()
	if err != nil {
		return err
	}

	resource, err := lib.PromptsGetOrSelect(ctx)
	if err != nil {
		return err
	}

	auth, err := authClient.Load()
	if err != nil {
		return err
	}

	repoName := resource.Get().RepoName()
	if err = auth.RegisterRepository(resource.Get().RepoID()); err != nil {
		return repositoryI18n.RegisteringRepositoryFailed(repoName, err)
	}

	repositoryI18n.Imported(repoName, profile.Cloud)
	repositoryI18n.TriggerBuild()

	return nil
}
