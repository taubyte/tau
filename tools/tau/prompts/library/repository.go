package libraryPrompts

import (
	"fmt"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/flags"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	repositoryLib "github.com/taubyte/tau/tools/tau/lib/repository"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/templates"
	"github.com/urfave/cli/v2"
)

func RepositoryInfo(ctx *cli.Context, library *structureSpec.Library, new bool) (any, error) {
	if new && prompts.GetGenerateRepository(ctx) {
		return repositoryInfoGenerate(ctx, library)
	}

	selectedRepository, err := prompts.SelectARepository(ctx, &repositoryLib.Info{
		Type:     repositoryLib.LibraryRepositoryType,
		FullName: library.RepoName,
		ID:       library.RepoID,
	})
	if err != nil {
		return nil, err
	}

	library.RepoID = selectedRepository.ID
	library.RepoName = selectedRepository.FullName

	projectConfig, err := projectLib.SelectedProjectConfig()
	if err != nil {
		return nil, err
	}

	if !selectedRepository.HasBeenCloned(projectConfig, library.Provider) {
		selectedRepository.DoClone = prompts.GetClone(ctx)
	}

	return selectedRepository, nil

}

func repositoryInfoGenerate(ctx *cli.Context, library *structureSpec.Library) (*repositoryLib.InfoTemplate, error) {
	repositoryName := fmt.Sprintf(common.LibraryRepoPrefix, library.Name)

	// Skipping prompt for repository name unless set, using generated name
	if ctx.IsSet(flags.RepositoryName.Name) {
		var err error
		repositoryName, err = prompts.GetOrRequireARepositoryName(ctx)
		if err != nil {
			return nil, err
		}
	}

	private := prompts.GetPrivate(ctx)

	templateMap, err := templates.Get().Libraries()
	if err != nil {
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
		},
		Private: private,
	}, nil
}
