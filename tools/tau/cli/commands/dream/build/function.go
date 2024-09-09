package build

import (
	"path"

	commonSpec "github.com/taubyte/tau/pkg/specs/common"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	dreamI18n "github.com/taubyte/tau/tools/tau/i18n/dream"
	applicationLib "github.com/taubyte/tau/tools/tau/lib/application"
	dreamLib "github.com/taubyte/tau/tools/tau/lib/dream"
	functionPrompts "github.com/taubyte/tau/tools/tau/prompts/function"
	"github.com/urfave/cli/v2"
)

func buildFunction(ctx *cli.Context) error {
	if !dreamLib.IsRunning() {
		dreamI18n.Help().IsDreamlandRunning()
		return dreamI18n.ErrorDreamlandNotStarted
	}

	function, err := functionPrompts.GetOrSelect(ctx)
	if err != nil {
		return err
	}

	builder, err := initBuild()
	if err != nil {
		return err
	}

	compileFor := &dreamLib.CompileForDFunc{
		ProjectId:  builder.project.Get().Id(),
		ResourceId: function.Id,
		Branch:     builder.currentBranch,
		Call:       function.Call,
	}

	if len(builder.selectedApp) > 0 {
		app, err := applicationLib.Get(builder.selectedApp)
		if err != nil {
			return err
		}

		compileFor.ApplicationId = app.Id
		compileFor.Path = path.Join(builder.projectConfig.CodeLoc(), commonSpec.ApplicationPathVariable.String(), builder.selectedApp, functionSpec.PathVariable.String(), function.Name)
	} else {
		compileFor.Path = path.Join(builder.projectConfig.CodeLoc(), functionSpec.PathVariable.String(), function.Name)
	}

	return compileFor.Execute()
}
