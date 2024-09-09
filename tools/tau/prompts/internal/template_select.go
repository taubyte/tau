package main

import (
	"github.com/pterm/pterm"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	smartopsSpec "github.com/taubyte/tau/pkg/specs/smartops"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/singletons/templates"
	"github.com/urfave/cli/v2"
)

var templateTypeFlag = &cli.StringFlag{
	Name: "type",
}

func GetTemplateType(ctx *cli.Context) string {
	if !ctx.IsSet(templateTypeFlag.Name) {
		selected, err := prompts.SelectInterface([]string{functionSpec.PathVariable.String(), websiteSpec.PathVariable.String(), smartopsSpec.PathVariable.String()}, "Template type:", "")
		if err != nil {
			panic(err)
		}

		return selected
	}

	return ctx.String(templateTypeFlag.Name)
}

var TemplateCommand = &cli.Command{
	Name: "template_select",
	Flags: flags.Combine(
		flags.Template,
		templateTypeFlag,
		flags.Language,
	),
	Action: func(ctx *cli.Context) (err error) {
		var templateMap map[string]templates.TemplateInfo

		switch GetTemplateType(ctx) {
		case functionSpec.PathVariable.String():
			language, err := prompts.GetOrSelectLanguage(ctx)
			if err != nil {
				return err
			}
			templateMap, err = templates.Get().Function(language)
			if err != nil {
				return err
			}
		case smartopsSpec.PathVariable.String():
			language, err := prompts.GetOrSelectLanguage(ctx)
			if err != nil {
				return err
			}
			templateMap, err = templates.Get().SmartOps(language)
			if err != nil {
				return err
			}
		case websiteSpec.PathVariable.String():
			templateMap, err = templates.Get().Websites()
			if err != nil {
				return err
			}
		}

		// New
		var templateUrl string
		templateUrl, err = prompts.SelectATemplate(ctx, templateMap)
		if err != nil {
			return err
		}

		// Edit, sending empty cli context so that the flags are not set
		templateUrl, err = prompts.SelectATemplate(&cli.Context{}, templateMap, templateUrl)
		if err != nil {
			return err
		}

		pterm.Success.Printfln("Got template: `%s`", templateUrl)
		return nil
	},
}
