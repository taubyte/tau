package common

import "github.com/urfave/cli/v2"

func Base(cmd *cli.Command, ops ...Option) (*cli.Command, []Option) {
	return cmd, ops
}
