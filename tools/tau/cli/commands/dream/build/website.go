package build

import (
	"fmt"
	"os"
	"path"
	"strings"

	dreamI18n "github.com/taubyte/tau/tools/tau/i18n/dream"
	websiteI18n "github.com/taubyte/tau/tools/tau/i18n/website"
	applicationLib "github.com/taubyte/tau/tools/tau/lib/application"
	dreamLib "github.com/taubyte/tau/tools/tau/lib/dream"
	websitePrompts "github.com/taubyte/tau/tools/tau/prompts/website"
	"github.com/urfave/cli/v2"
)

func buildWebsite(ctx *cli.Context) error {
	if !dreamLib.IsRunning() {
		dreamI18n.Help().IsDreamlandRunning()
		return dreamI18n.ErrorDreamlandNotStarted
	}

	website, err := websitePrompts.GetOrSelect(ctx)
	if err != nil {
		return err
	}

	builder, err := initBuild()
	if err != nil {
		return err
	}

	compileFor := &dreamLib.CompileForRepository{
		ProjectId:  builder.project.Get().Id(),
		ResourceId: website.Id,
		Branch:     builder.currentBranch,
	}

	if len(builder.selectedApp) > 0 {
		app, err := applicationLib.Get(builder.selectedApp)
		if err != nil {
			return err
		}

		compileFor.ApplicationId = app.Id
	}

	splitName := strings.Split(website.RepoName, "/")
	if len(splitName) != 2 {
		return fmt.Errorf("invalid repository name `%s` expected `user/repo`", website.RepoName)
	}

	compileFor.Path = path.Join(builder.projectConfig.WebsiteLoc(), splitName[1])
	_, err = os.Stat(compileFor.Path)
	if err != nil {
		websiteI18n.Help().BeSureToCloneWebsite()
		return err
	}

	return compileFor.Execute()
}
