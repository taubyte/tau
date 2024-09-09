package build

import (
	"fmt"
	"os"
	"path"
	"strings"

	dreamI18n "github.com/taubyte/tau/tools/tau/i18n/dream"
	libraryI18n "github.com/taubyte/tau/tools/tau/i18n/library"
	applicationLib "github.com/taubyte/tau/tools/tau/lib/application"
	dreamLib "github.com/taubyte/tau/tools/tau/lib/dream"
	libraryPrompts "github.com/taubyte/tau/tools/tau/prompts/library"
	"github.com/urfave/cli/v2"
)

func buildLibrary(ctx *cli.Context) error {
	if !dreamLib.IsRunning() {
		dreamI18n.Help().IsDreamlandRunning()
		return dreamI18n.ErrorDreamlandNotStarted
	}

	library, err := libraryPrompts.GetOrSelect(ctx)
	if err != nil {
		return err
	}

	builder, err := initBuild()
	if err != nil {
		return err
	}

	compileFor := &dreamLib.CompileForRepository{
		ProjectId:  builder.project.Get().Id(),
		ResourceId: library.Id,
		Branch:     builder.currentBranch,
	}

	if len(builder.selectedApp) > 0 {
		app, err := applicationLib.Get(builder.selectedApp)
		if err != nil {
			return err
		}

		compileFor.ApplicationId = app.Id
	}

	splitName := strings.Split(library.RepoName, "/")
	if len(splitName) != 2 {
		return fmt.Errorf("invalid repository name `%s` expected `user/repo`", library.RepoName)
	}

	compileFor.Path = path.Join(builder.projectConfig.LibraryLoc(), splitName[1])
	_, err = os.Stat(compileFor.Path)
	if err != nil {
		libraryI18n.Help().BeSureToCloneLibrary()
		return err
	}

	return compileFor.Execute()
}
