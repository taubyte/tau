package common

import (
	"github.com/urfave/cli/v2"
)

type linker struct {
	parent *cli.Command
	raw    *cli.Command
	ops    []Option
}

func Create(cmd *cli.Command, ops ...Option) Command {
	return &linker{nil, cmd, ops}
}

func (l *linker) Raw() *cli.Command {
	return l.raw
}

func (l *linker) Parent() *cli.Command {
	return l.parent
}

func (l *linker) Options() []Option {
	return l.ops
}

func setBaseCmdFields(base *cli.Command, cmd *cli.Command) {
	if cmd.Name == "" {
		cmd.Name = base.Name
	}

	if cmd.ArgsUsage == "" {
		cmd.ArgsUsage = base.ArgsUsage
	}

	if len(cmd.Aliases) == 0 {
		cmd.Aliases = base.Aliases
	}
}

/*
Initialize will run the options of a command and return a command using the base command

Example: tau new application
  - parentCmd: new
  - baseCmd: application
*/
func (l *linker) Initialize(parentCmd *cli.Command, baseCmd *cli.Command, baseOps []Option) *cli.Command {
	l.parent = parentCmd

	setBaseCmdFields(baseCmd, l.raw)

	for _, op := range append(baseOps, l.ops...) {
		op(l)
	}

	return l.raw
}

func (l *linker) Linker() Linker {
	return l
}
