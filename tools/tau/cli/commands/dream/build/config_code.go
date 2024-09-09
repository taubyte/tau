package build

import (
	dreamI18n "github.com/taubyte/tau/tools/tau/i18n/dream"
	dreamLib "github.com/taubyte/tau/tools/tau/lib/dream"
	"github.com/urfave/cli/v2"
)

func executeConfigCode(config bool, code bool) error {
	if !dreamLib.IsRunning() {
		dreamI18n.Help().IsDreamlandRunning()
		return dreamI18n.ErrorDreamlandNotStarted
	}

	builder, err := initBuild()
	if err != nil {
		return err
	}

	return dreamLib.BuildLocalConfigCode{
		Config:      config,
		Code:        code,
		Branch:      builder.currentBranch,
		ProjectPath: builder.projectConfig.Location,
		ProjectID:   builder.project.Get().Id(),
	}.Execute()
}

func buildConfig(ctx *cli.Context) error {
	return executeConfigCode(true, false)
}

func buildCode(ctx *cli.Context) error {
	return executeConfigCode(false, true)
}
