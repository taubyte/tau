package options

import (
	"fmt"

	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/i18n"
	"github.com/urfave/cli/v2"
)

func SetFlagAsArgs0(flag string) func(ctx *cli.Context) error {
	return func(ctx *cli.Context) error {
		first := ctx.Args().First()
		if len(first) == 0 {
			return nil
		}

		return ctx.Set(flag, first)
	}
}

func FlagArg0(flag string) common.Option {
	return func(l common.Linker) {
		// Insert name flag into first position
		l.Flags().Shift(flags.Name)
		l.Before().Shift(SetFlagAsArgs0(flag))
	}
}

func SetNameAsArgs0(ctx *cli.Context) error {
	first := ctx.Args().First()
	if len(first) == 0 {
		return nil
	}

	return ctx.Set(flags.Name.Name, first)
}

func NameFlagArg0() common.Option {
	return func(l common.Linker) {
		// Insert name flag into first position
		l.Flags().Shift(flags.Name)
		l.Before().Shift(SetNameAsArgs0)
	}
}

func NameFlagSelectedArg0(selected string) common.Option {
	return func(l common.Linker) {
		NameFlagArg0()(l)

		parentName := l.Parent().Name

		if parentName != "new" && parentName != "select" {
			l.Raw().ArgsUsage = fmt.Sprintf(i18n.ArgsUsageNameDefaultSelected, selected)
			l.Raw().Flags[0] = &cli.StringFlag{
				Name:        flags.Name.Name,
				Aliases:     flags.Name.Aliases,
				Usage:       "Will default to selected",
				DefaultText: selected,
			}
		}
	}
}
