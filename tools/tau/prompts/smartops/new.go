package smartopsPrompts

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	smartopsLib "github.com/taubyte/tau/tools/tau/lib/smartops"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func New(ctx *cli.Context) (smartops *structureSpec.SmartOp, templateURL string, err error) {
	smartops = &structureSpec.SmartOp{}

	taken, err := smartopsLib.List()
	if err != nil {
		return
	}

	smartops.Name = prompts.GetOrRequireAUniqueName(ctx, NamePrompt, taken)

	templateURL, err = checkTemplate(ctx, smartops)
	if err != nil {
		return
	}

	smartops.Description = prompts.GetOrAskForADescription(ctx, smartops.Description)
	smartops.Tags = prompts.GetOrAskForTags(ctx, smartops.Tags)

	smartops.Timeout, err = prompts.GetOrRequireATimeout(ctx, smartops.Timeout)
	if err != nil {
		return
	}

	smartops.Memory, err = prompts.GetOrRequireMemoryAndType(ctx, smartops.Memory == 0, smartops.Memory)
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
