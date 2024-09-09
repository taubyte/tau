package logs

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/cli/common/options"
	"github.com/urfave/cli/v2"
)

func (link) Base() (*cli.Command, []common.Option) {
	return common.Base(&cli.Command{
		Name:    "logs",
		Aliases: []string{"log"},
	}, options.FlagArg0("jid"))
}
