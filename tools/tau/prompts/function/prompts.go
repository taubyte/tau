package functionPrompts

import (
	cliCommon "github.com/taubyte/tau/pkg/cli/common"
	"github.com/taubyte/tau/tools/tau/common"
	functionFlags "github.com/taubyte/tau/tools/tau/flags/function"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func GetFunctionType(ctx *cli.Context, prev ...string) (string, error) {
	return prompts.SelectInterfaceField(ctx,
		common.FunctionTypes,
		functionFlags.Type.Name,
		TypePrompt,
		prev...,
	)
}

func GetHttpMethod(ctx *cli.Context, prev ...string) (string, error) {
	return prompts.SelectInterfaceField(ctx,
		cliCommon.HTTPMethodTypes,
		functionFlags.Method.Name,
		MethodPrompt,
		prev...,
	)
}

func GetOrRequireACommand(ctx *cli.Context, prev ...string) string {
	return prompts.GetOrRequireAString(ctx,
		functionFlags.Command.Name,
		CommandPrompt,
		nil,
		prev...,
	)
}

func GetOrRequireAChannel(ctx *cli.Context, prev ...string) string {
	return prompts.GetOrRequireAString(ctx,
		functionFlags.Channel.Name,
		ChannelPrompt,
		nil,
		prev...,
	)
}
