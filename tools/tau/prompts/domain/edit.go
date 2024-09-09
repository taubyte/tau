package domainPrompts

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func Edit(ctx *cli.Context, prev *structureSpec.Domain) error {
	prev.Description = prompts.GetOrAskForADescription(ctx, prev.Description)
	prev.Tags = prompts.GetOrAskForTags(ctx, prev.Tags)
	prev.Fqdn = GetOrRequireAnFQDN(ctx, prev.Fqdn)

	err := certificate(ctx, prev, false)
	if err != nil {
		return err
	}

	return nil
}
