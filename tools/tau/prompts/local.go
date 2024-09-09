package prompts

import (
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
)

func GetOrAskForLocal(ctx *cli.Context, prev ...bool) bool {
	return GetOrAskForBool(ctx, flags.Local.Name, LocalPrompt, prev...)
}
