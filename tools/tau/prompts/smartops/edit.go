package smartopsPrompts

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func Edit(ctx *cli.Context, smartops *structureSpec.SmartOp) (err error) {
	smartops.Description = prompts.GetOrAskForADescription(ctx, smartops.Description)
	smartops.Tags = prompts.GetOrAskForTags(ctx, smartops.Tags)

	smartops.Timeout, err = prompts.GetOrRequireATimeout(ctx, smartops.Timeout)
	if err != nil {
		return
	}

	smartops.Memory, err = prompts.GetOrRequireMemoryAndType(ctx, false, smartops.Memory)
	if err != nil {
		return
	}

	source, err := prompts.GetOrSelectSource(ctx, smartops.Source)
	if err != nil {
		return
	}
	smartops.Source = source.String()

	smartops.Call = prompts.GetOrRequireACall(ctx, source, smartops.Call)

	return
}
