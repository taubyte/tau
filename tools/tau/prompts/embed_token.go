package prompts

import (
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
)

func GetOrAskForEmbedToken(ctx *cli.Context, prev ...bool) bool {
	return GetOrAskForBool(ctx, flags.EmbedToken.Name, EmbedTokenPrompt, prev...)
}
