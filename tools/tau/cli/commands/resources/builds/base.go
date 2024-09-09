package builds

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/urfave/cli/v2"
)

func (link) Base() (*cli.Command, []common.Option) {
	return common.Base(&cli.Command{
		Name:    "builds",
		Usage:   "lists jobs within a time range (default: 7 days)",
		Aliases: []string{"jobs"},
	})
}
