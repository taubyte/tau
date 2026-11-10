package project

import (
	"fmt"
	"strings"

	"github.com/google/go-github/v71/github"
	httpClient "github.com/taubyte/tau/clients/http/auth"
	"github.com/taubyte/tau/pkg/cli/i18n"
	"github.com/taubyte/tau/tools/tau/cli/common"
	authClient "github.com/taubyte/tau/tools/tau/clients/auth_client"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	repositoryI18n "github.com/taubyte/tau/tools/tau/i18n/repository"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/shared"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/session"
	slices "github.com/taubyte/tau/utils/slices/string"
	"github.com/urfave/cli/v2"
)

func (link) Import() common.Command {
	return common.Create(
		&cli.Command{
			Action: _import,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name: "config",
				},
				&cli.StringFlag{
					Name: "code",
				},
			},
		},
	)
}

func _import(ctx *cli.Context) error {
	if cloud, _ := session.GetSelectedCloud(); len(cloud) < 1 {
		return singletonsI18n.NoCloudSelected()
	}

	profile, err := loginLib.GetSelectedProfile()
	if err != nil {
		return err
	}

	// Named repositories are looked up by name. Listing every repository took
	// several GitHub calls on a large account, and one failed page failed the
	// import; the list is only needed to offer a choice.
	var repos []*github.Repository
	if ctx.String("config") != "" && ctx.String("code") != "" {
		for _, name := range []string{ctx.String("config"), ctx.String("code")} {
			repo, err := GetRepo(ctx.Context, profile.Token, name)
			if err != nil {
				return repositoryI18n.ErrorListRepositories(profile.GitUsername, err)
			}
			repos = append(repos, repo)
		}
	} else if repos, err = ListRepos(ctx.Context, profile.Token, profile.GitUsername); err != nil {
		return err
	}

	repoMap := make(map[string]*github.Repository, len(repos))
	configRepos := make([]string, 0, len(repos))
	codeRepos := make([]string, 0, len(repos))
	for _, repo := range repos {
		splitName := strings.SplitN(repo.GetName(), "_", 3)
		if len(splitName) < 2 || splitName[0] != "tb" || splitName[1] == "library" || splitName[1] == "website" {
			continue
		}

		fullName := repo.GetFullName()
		switch splitName[1] {
		case "code":
			codeRepos = append(codeRepos, fullName)
		default:
			configRepos = append(configRepos, fullName)
		}
		repoMap[fullName] = repo
	}

	configRepoName, err := prompts.GetOrAskForSelection(ctx, "config", "Config:", configRepos)
	if err != nil {
		return err
	}
	configSplit := strings.SplitN(configRepoName, "_", 2)
	codeSplit := []string{configSplit[0], "code", configSplit[1]}
	codeRepoName := strings.Join(codeSplit, "_")

	var prev []string
	if slices.Contains(codeRepos, codeRepoName) {
		prev = append(prev, codeRepoName)
	}

	codeRepoName, err = prompts.GetOrAskForSelection(ctx, "code", "Code:", codeRepos, prev...)
	if err != nil {
		return err
	}
	codeRepo, ok := repoMap[codeRepoName]
	if !ok {
		return i18n.ErrorDoesNotExist("code repo", codeRepoName)
	}

	configRepo, ok := repoMap[configRepoName]
	if !ok {
		return i18n.ErrorDoesNotExist("config repo", configRepoName)
	}

	clientProject := &httpClient.Project{
		Name: configSplit[1],
	}

	auth, err := authClient.Load()
	if err != nil {
		return err
	}

	// A project with an id keeps it: its builds are filed under the id in its
	// config, so a cloud that gave it a new one served none of them. Read it
	// before registering anything, so a repository without one fails clean.
	projectID, err := ConfigProjectID(ctx.Context, profile.Token, configRepoName)
	if err != nil {
		return projectI18n.CreatingProjectFailed(fmt.Errorf("reading the project id from %s failed with: %w", configRepoName, err))
	}

	codeId := fmt.Sprintf("%d", codeRepo.GetID())
	configId := fmt.Sprintf("%d", configRepo.GetID())

	if err = auth.RegisterRepository(codeId); err != nil {
		return repositoryI18n.RegisteringRepositoryFailed(codeRepoName, err)
	}

	if err = auth.RegisterRepository(configId); err != nil {
		return repositoryI18n.RegisteringRepositoryFailed(configRepoName, err)
	}

	if projectID = importableID(auth, projectID, configRepoName); projectID != "" {
		err = clientProject.Import(auth, projectID, configId, codeId)
		if err != nil {
			// The cloud refuses an id held by a project the caller cannot write to.
			printer.Out.WarningPrintfln("importing under id %s failed (%s); registering a new project", projectID, err)
			err = clientProject.Create(auth, configId, codeId)
		}
	} else {
		err = clientProject.Create(auth, configId, codeId)
	}
	if err != nil {
		return projectI18n.CreatingProjectFailed(err)
	}

	reportImported(clientProject.Name)

	if prompts.ConfirmPrompt(ctx, fmt.Sprintf("select `%s` as current project?", clientProject.Name)) {
		if err = session.Set().SelectedProject(clientProject.Name); err != nil {
			return projectI18n.SelectingAProjectPromptFailed(err)
		}

		projectI18n.SelectedProject(clientProject.Name)
		checkProjectClonedHelp(clientProject.Name)
	}

	return nil
}

// importableID returns the id to import under, or "" to create a new project.
// An id the cloud already holds for another config repository belongs to
// another project (this one is a fork or a copy): importing under it would be
// refused, or on Dream overwrite that project.
func importableID(auth *httpClient.Client, projectID, configRepoName string) string {
	if projectID == "" {
		return ""
	}
	existing, err := auth.GetProjectById(projectID)
	if err != nil || existing.RepoList == nil || existing.RepoList.Configuration.Fullname == configRepoName {
		return projectID
	}
	printer.Out.WarningPrintfln("%s carries the id of project %s, which this cloud holds for %s; registering a new project", configRepoName, projectID, existing.RepoList.Configuration.Fullname)
	return ""
}

// reportImported names the cloud the session selected, not the profile's.
func reportImported(name string) {
	projectI18n.ImportedProject(name, session.CloudName())
}
