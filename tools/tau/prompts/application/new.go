package applicationPrompts

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	applicationLib "github.com/taubyte/tau/tools/tau/lib/application"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func New(ctx *cli.Context) (*structureSpec.App, error) {
	app := &structureSpec.App{}

	taken, err := applicationLib.List()
	if err != nil {
		return nil, err
	}

	app.Name = prompts.GetOrRequireAUniqueName(ctx, NamePrompt, taken)

	app.Description = prompts.GetOrAskForADescription(ctx)
	app.Tags = prompts.GetOrAskForTags(ctx)

	return app, nil
}
