package servicePrompts

import (
	serviceFlags "github.com/taubyte/tau/tools/tau/flags/service"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/validate"
	"github.com/urfave/cli/v2"
)

func GetOrRequireAProtocol(c *cli.Context, prev ...string) string {
	return prompts.RequiredStringWithValidator(c, ProtocolPrompt, func(*cli.Context, string, ...string) (ret string) {
		return prompts.GetOrAskForAStringValue(c, serviceFlags.Protocol.Name, ProtocolPrompt, prev...)
	}, validate.VariableMatchValidator)
}
