package common

import (
	"github.com/urfave/cli/v2"
)

type Option func(Linker)

type Command interface {
	Raw() *cli.Command
	Parent() *cli.Command
	Options() []Option
	Initialize(parentCommand *cli.Command, baseCommand *cli.Command, baseOps []Option) *cli.Command
}

type Basic interface {
	New() Command
	Edit() Command
	Delete() Command
	Query() Command
	List() Command
	Select() Command
	Clone() Command
	Push() Command
	Pull() Command
	Checkout() Command
	Import() Command

	// Sets the following in the command if not already set:
	// Name
	// Aliases
	// Also runs the options provided
	Base() (*cli.Command, []Option)
}

type Linker interface {
	Command
	Before() BeforeHandler
	Flags() FlagHandler
}

type BeforeHandler interface {
	Shift(method cli.BeforeFunc)
	Push(method cli.BeforeFunc)
}

type FlagHandler interface {
	Shift(flags ...cli.Flag)
	Push(flags ...cli.Flag)
}
