package project

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/pterm/pterm"
	client "github.com/taubyte/tau/clients/http/auth"
	authCommon "github.com/taubyte/tau/clients/http/auth/git/common"
	"github.com/taubyte/tau/pkg/cli/i18n"
	"github.com/taubyte/tau/tools/tau/cli/common"
	authClient "github.com/taubyte/tau/tools/tau/clients/auth_client"
	"github.com/taubyte/tau/tools/tau/config"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	repositoryI18n "github.com/taubyte/tau/tools/tau/i18n/repository"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/urfave/cli/v2"
)

func (link) Delete() common.Command {
	return common.Create(
		&cli.Command{
			Action: _delete,
		},
	)
}

func _delete(ctx *cli.Context) error {
	profile, err := loginLib.GetSelectedProfile()
	if err != nil {
		return err
	}

	project, store, err := selectDeletion(ctx)
	if err != nil {
		return err
	}

	repos, err := project.Repositories()
	if err != nil {
		return err
	}

	repoNames := []string{repos.Code.Fullname, repos.Configuration.Fullname}

	msg := formatDeleteConfirm(project.Name, repoNames)
	if prompts.ConfirmPrompt(ctx, msg) {
		resources, err := store.RepositoryNames()
		if err != nil {
			return err
		}

		if len(resources) > 0 {
			repoNames = append(repoNames,
				prompts.MultiSelect(ctx,
					prompts.MultiSelectConfig{
						Field:   "resource",
						Prompt:  "Select additional resources to unregister",
						Options: resources,
					},
				)...,
			)
		}

		if _, err = project.Delete(); err != nil {
			return projectI18n.ErrorDeleteProject(project.Name, err)
		}

		prj, err := config.Projects().Get(project.Name)
		if err == nil {
			if err = os.RemoveAll(prj.Location); err == nil {
				pterm.Success.Println("Removed", prj.Location)
			}
		}

		auth, err := authClient.Load()
		if err != nil {
			return err
		}

		if err = unregister(auth, repoNames); err != nil {
			return repositoryI18n.ErrorUnregisterRepositories(err)
		}

		projectI18n.RemovedProject(project.Name, profile.Cloud)

		repoNames = prompts.MultiSelect(ctx, prompts.MultiSelectConfig{
			Field:   "github",
			Prompt:  "Remove from github?",
			Options: repoNames,
		})

		token := profile.Token
		userName := profile.GitUsername
		for _, name := range repoNames {
			name = strings.TrimPrefix(name, userName+"/")
			if _err := removeFromGithub(ctx.Context, token, userName, name); _err != nil {
				if err != nil {
					err = fmt.Errorf("%s:%w", err, _err)
				} else {
					err = _err
				}
			}
		}
		if err != nil {
			if errors.Is(err, repositoryI18n.ErrorAdminRights) {
				pterm.Info.Println("Delete repositories manually")
				return nil
			}

			return err
		}
	}

	return nil
}

func selectDeletion(ctx *cli.Context) (*client.Project, *tcc.Store, error) {
	projects, err := projectLib.ListResources()
	if err != nil {
		return nil, nil, err
	}

	// TODO: Avoid making a list and map by adding Prompt: that will parse Name() or String() methods
	projectMap := make(map[string]*client.Project, len(projects))
	projectList := make([]string, len(projects))
	for idx, project := range projects {
		projectList[idx] = project.Name
		projectMap[project.Name] = project
	}

	projectName, err := prompts.GetOrAskForSelection(ctx, "name", "Project:", projectList)
	if err != nil {
		return nil, nil, err
	}
	project, ok := projectMap[projectName]
	if !ok {
		return nil, nil, i18n.ErrorDoesNotExist("project", projectName)
	}

	config, err := config.Projects().Get(projectName)
	if err != nil {
		return nil, nil, err
	}

	store, err := tcc.OpenAt(tcc.ConfigDir(config.Location))
	if err != nil {
		return nil, nil, err
	}

	return project, store, nil
}

func formatDeleteConfirm(project string, repos []string) string {
	formattedMessage := fmt.Sprintf(
		"Removing project `%s` will unregister the following repositories from auth:",
		pterm.FgCyan.Sprint(project),
	)

	for _, repo := range repos {
		formattedMessage += fmt.Sprintf("\n%s", cyanBullet(repo))
	}

	formattedMessage += "\nProceed?"
	return formattedMessage
}

func cyanBullet(name string) string {
	return fmt.Sprintf(" \u2022 %s", pterm.FgCyan.Sprint(name))
}

func unregister(auth *client.Client, names []string) error {
	repos := make([]authCommon.Repository, len(names))
	for idx, name := range names {
		repo, err := auth.GetRepositoryByName(name)
		if err != nil {
			return err
		}

		repos[idx] = repo
	}

	var err error
	for _, repo := range repos {
		if _err := auth.UnregisterRepository(repo.Get().ID()); _err != nil {
			if err != nil {
				err = fmt.Errorf("%s:%w", err, _err)
			} else {
				err = _err
			}
		}
	}

	return err
}
