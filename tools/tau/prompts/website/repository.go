package websitePrompts

import (
	"fmt"
	"strings"

	"github.com/pterm/pterm"
	httpClient "github.com/taubyte/tau/clients/http/auth"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/flags"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	repositoryLib "github.com/taubyte/tau/tools/tau/lib/repository"
	"github.com/taubyte/tau/tools/tau/prompts"
	authClient "github.com/taubyte/tau/tools/tau/singletons/auth_client"
	"github.com/taubyte/tau/tools/tau/singletons/templates"
	"github.com/urfave/cli/v2"
)

func RepositoryInfo(ctx *cli.Context, website *structureSpec.Website, new bool) (interface{}, error) {
	if new && prompts.GetGenerateRepository(ctx) {
		return repositoryInfoGenerate(ctx, website)
	}

	selectedRepository, err := prompts.SelectARepository(ctx, &repositoryLib.Info{
		Type:     repositoryLib.WebsiteRepositoryType,
		FullName: website.RepoName,
		ID:       website.RepoID,
	})
	if err != nil {
		return nil, err
	}

	website.RepoID = selectedRepository.ID
	website.RepoName = selectedRepository.FullName

	projectConfig, err := projectLib.SelectedProjectConfig()
	if err != nil {
		return nil, err
	}

	if !selectedRepository.HasBeenCloned(projectConfig, website.Provider) {
		selectedRepository.DoClone = prompts.GetClone(ctx)
	}

	return selectedRepository, nil

}

func isRepositoryNameTaken(client *httpClient.Client, name string) (bool, error) {
	var fullName string
	if len(strings.Split(name, "/")) == 2 {
		fullName = name
	} else {
		userInfo, err := client.User().Get()
		if err != nil {
			return false, err
		}

		fullName = fmt.Sprintf("%s/%s", userInfo.Login, name)
	}

	// Considering name to be taken if err is nil
	_, err := client.GetRepositoryByName(fullName)
	if err == nil {
		return true, nil
	}

	return false, nil
}

// Only called by new
func repositoryInfoGenerate(ctx *cli.Context, website *structureSpec.Website) (*repositoryLib.InfoTemplate, error) {
	var repositoryName string
	if ctx.IsSet(flags.RepositoryName.Name) {
		repositoryName = ctx.String(flags.RepositoryName.Name)
	} else {
		repositoryName = fmt.Sprintf(common.WebsiteRepoPrefix, website.Name)
	}

	// Confirm name is valid
	client, err := authClient.Load()
	if err != nil {
		return nil, err
	}

	// Skipping prompt for repository name unless set, or generated name is already taken
	for taken, err := isRepositoryNameTaken(client, repositoryName); taken; {
		if err != nil {
			return nil, err
		}

		pterm.Warning.Printfln("Repository name %s is already taken", repositoryName)
		repositoryName = prompts.GetOrRequireARepositoryName(ctx)

		taken, err = isRepositoryNameTaken(client, repositoryName)
	}

	private := prompts.GetPrivate(ctx)

	templateMap, err := templates.Get().Websites()
	if err != nil {
		// TODO verbose
		return nil, err
	}

	templateUrl, err := prompts.SelectATemplate(ctx, templateMap)
	if err != nil {
		return nil, err
	}

	return &repositoryLib.InfoTemplate{
		RepositoryName: repositoryName,
		Info: templates.TemplateInfo{
			URL: templateUrl,
			// TODO Update website template description style
			// Description: website.Description,
		},
		Private: private,
	}, nil
}
