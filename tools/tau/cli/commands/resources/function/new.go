package function

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	functionFlags "github.com/taubyte/tau/tools/tau/flags/function"
	functionI18n "github.com/taubyte/tau/tools/tau/i18n/function"
	functionLib "github.com/taubyte/tau/tools/tau/lib/function"
	functionPrompts "github.com/taubyte/tau/tools/tau/prompts/function"
	functionTable "github.com/taubyte/tau/tools/tau/table/function"
	"github.com/urfave/cli/v2"
)

func (link) New() common.Command {
	var templateURL string
	return (&resources.New[*structureSpec.Function]{
		PromptsNew: func(ctx *cli.Context) (*structureSpec.Function, error) {
			function, _templateURL, err := functionPrompts.New(ctx)
			templateURL = _templateURL
			return function, err
		},
		TableConfirm:      functionTable.Confirm,
		PromptsCreateThis: functionPrompts.CreateThis,
		LibNew: func(function *structureSpec.Function) error {
			return functionLib.New(function, templateURL)
		},
		I18nCreated: functionI18n.Created,

		UniqueFlags: flags.Combine(
			flags.Timeout,
			flags.Memory,
			flags.MemoryUnit,
			functionFlags.Type,
			flags.Source,
			flags.Call,
			flags.Template,
			flags.Language,
			flags.UseCodeTemplate,
			functionFlags.Http(),

			// P2P and PubSub
			flags.Local,
			functionFlags.P2P(),
			functionFlags.PubSub(),
		),
	}).Default()
}
