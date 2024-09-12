package servicePrompts

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	serviceLib "github.com/taubyte/tau/tools/tau/lib/service"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func New(ctx *cli.Context) (*structureSpec.Service, error) {
	service := &structureSpec.Service{}

	taken, err := serviceLib.List()
	if err != nil {
		return nil, err
	}

	service.Name = prompts.GetOrRequireAUniqueName(ctx, NamePrompt, taken)
	service.Description = prompts.GetOrAskForADescription(ctx)
	service.Tags = prompts.GetOrAskForTags(ctx)
	service.Protocol = GetOrRequireAProtocol(ctx)

	return service, nil
}
