package smartopsPrompts

import (
	"os"
	"path"

	smartopsSchema "github.com/taubyte/tau/pkg/schema/smartops"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/singletons/templates"
	"github.com/urfave/cli/v2"
)

// Should only be used in new, will overwrite values.
func checkTemplate(ctx *cli.Context, smartops *structureSpec.SmartOp) (templateURL string, err error) {
	if !ctx.IsSet(flags.Template.Name) && !prompts.GetUseACodeTemplate(ctx) {
		return
	}

	language, err := prompts.GetOrSelectLanguage(ctx)
	if err != nil {
		return
	}

	codeTemplateMap, err := templates.Get().SmartOps(language)
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

	getter, err := smartopsSchema.Yaml(smartops.Name, "", file)
	if err != nil {
		return
	}

	_smartops, err := getter.Struct()
	if err != nil {
		return
	}

	// Overwrite new smartops
	*smartops = *_smartops

	return
}
