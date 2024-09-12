package messagingPrompts

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func Edit(ctx *cli.Context, prev *structureSpec.Messaging) error {
	prev.Description = prompts.GetOrAskForADescription(ctx, prev.Description)
	prev.Tags = prompts.GetOrAskForTags(ctx, prev.Tags)

	prev.Local = prompts.GetOrAskForLocal(ctx, prev.Local)
	prev.Regex = prompts.GetMatchRegex(ctx, prev.Regex)
	prev.Match = GetOrRequireAChannelMatch(ctx, prev.Match)
	prev.MQTT = GetMQTT(ctx, prev.MQTT)
	prev.WebSocket = GetWebSocket(ctx, prev.WebSocket)

	return nil
}
