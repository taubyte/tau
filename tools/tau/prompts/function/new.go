package functionPrompts

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/common"
	functionLib "github.com/taubyte/tau/tools/tau/lib/function"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func New(ctx *cli.Context) (function *structureSpec.Function, templateURL string, err error) {
	function = &structureSpec.Function{}

	taken, err := functionLib.List()
	if err != nil {
		return
	}

	function.Name = prompts.GetOrRequireAUniqueName(ctx, NamePrompt, taken)

	templateURL, err = checkTemplate(ctx, function)
	if err != nil {
		return
	}

	function.Description = prompts.GetOrAskForADescription(ctx, function.Description)
	function.Tags = prompts.GetOrAskForTags(ctx, function.Tags)

	function.Timeout, err = prompts.GetOrRequireATimeout(ctx, function.Timeout)
	if err != nil {
		return
	}

	function.Memory, err = prompts.GetOrRequireMemoryAndType(ctx, function.Memory == 0, function.Memory)
	if err != nil {
		return
	}

	function.Type, err = GetFunctionType(ctx, function.Type)
	if err != nil {
		return
	}

	switch function.Type {
	case common.FunctionTypeHttp:
		err = editHttp(ctx, function)
	case common.FunctionTypeHttps:
		function.Secure = true
		err = editHttp(ctx, function)
	case common.FunctionTypeP2P:
		err = editP2P(ctx, function)
	case common.FunctionTypePubSub:
		err = editPubSub(ctx, function)
	}
	if err != nil {
		return
	}

	source, err := prompts.GetOrSelectSource(ctx, function.Source)
	if err != nil {
		return
	}

	function.Source = source.String()

	function.Call = prompts.GetOrRequireACall(ctx, source, function.Call)

	return
}
