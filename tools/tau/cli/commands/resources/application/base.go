package application

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/cli/common/options"
	"github.com/taubyte/tau/tools/tau/env"
	"github.com/taubyte/tau/tools/tau/i18n"
	"github.com/urfave/cli/v2"
)

func (link) Base() (*cli.Command, []common.Option) {
	selected, exist := env.GetSelectedApplication()
	if !exist {
		selected = "selected"
	}

	return common.Base(
		&cli.Command{
			Name:      "application",
			Aliases:   []string{"app"},
			ArgsUsage: i18n.ArgsUsageName,
		}, options.NameFlagSelectedArg0(selected),
	)
}
