package functionPrompts

import (
	"fmt"
	"os"
	"path"

	"github.com/taubyte/tau/pkg/schema/functions"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/flags"
	functionFlags "github.com/taubyte/tau/tools/tau/flags/function"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/templates"
	"github.com/urfave/cli/v2"
)

// Should only be used in new, will overwrite values.
func checkTemplate(ctx *cli.Context, function *structureSpec.Function) (templateURL string, err error) {
	if !ctx.IsSet(flags.Template.Name) && !prompts.GetUseACodeTemplate(ctx) {
		return
	}

	language, err := prompts.GetOrSelectLanguage(ctx)
	if err != nil {
		return
	}

	codeTemplateMap, err := templates.Get().Function(language)
	if err != nil {
		return
	}

	templateURL, err = prompts.SelectATemplate(ctx, codeTemplateMap)
	if err != nil {
		return
	}

	file, err := os.ReadFile(path.Join(templateURL, "config.yaml"))
	if err != nil {
		return
	}

	getter, err := functions.Yaml(function.Name, "", file)
	if err != nil {
		return
	}

	_function, err := getter.Struct()
	if err != nil {
		return
	}

	// Overwrite new function
	*function = *_function

	return
}

func editHttp(ctx *cli.Context, function *structureSpec.Function) (err error) {
	function.Domains, err = prompts.GetOrSelectDomainsWithFQDN(ctx, function.Domains...)
	if err != nil {
		return
	}

	function.Method, err = GetHttpMethod(ctx, function.Method)
	if err != nil {
		return
	}

	fmt.Printf("[paths trace] function/helpers.go editHttp before RequiredPaths function.Paths=%q\n", function.Paths)
	function.Paths = prompts.RequiredPaths(ctx, function.Paths...)
	fmt.Printf("[paths trace] function/helpers.go editHttp after RequiredPaths function.Paths=%q\n", function.Paths)
	return
}

func editP2P(ctx *cli.Context, function *structureSpec.Function) (err error) {
	function.Protocol, err = prompts.SelectAServiceWithProtocol(ctx, functionFlags.Protocol.Name, ProtocolPrompt, function.Protocol)
	if err != nil {
		return
	}

	function.Command, err = GetOrRequireACommand(ctx, function.Command)
	if err != nil {
		return
	}

	function.Local = prompts.GetOrAskForLocal(ctx, function.Local)
	return
}

func editPubSub(ctx *cli.Context, function *structureSpec.Function) (err error) {
	function.Channel, err = GetOrRequireAChannel(ctx, function.Channel)
	if err != nil {
		return
	}
	function.Local = prompts.GetOrAskForLocal(ctx, function.Local)
	return
}
