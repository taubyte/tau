package projectPrompts

import (
	projectFlags "github.com/taubyte/tau/tools/tau/flags/project"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

/*
GetOrRequireVisibility parses public and private flags to then return
visible or the "public" bool.  This will error if both public and private are true
*/
func GetOrRequireVisibility(ctx *cli.Context) (visible bool, err error) {
	var (
		public, private bool
	)

	if ctx.IsSet(projectFlags.Private.Name) {
		private = ctx.Bool(projectFlags.Private.Name)
	}

	if ctx.IsSet(projectFlags.Public.Name) {
		public = ctx.Bool(projectFlags.Public.Name)
	}

	if public && private {
		return false, projectI18n.BothFlagsCannotBeTrue(projectFlags.Private.Name, projectFlags.Public.Name)
	}

	if !public && !private {
		selectedVisibility, err := prompts.SelectInterface(VisibilityOptions, projectVisibility, Public)
		if err != nil {
			return false, projectI18n.SelectingVisibilityFailed(err)
		}

		return selectedVisibility == Public, nil
	}

	return public, nil
}
