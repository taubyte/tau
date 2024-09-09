package projectFlags

import (
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	"github.com/urfave/cli/v2"
)

var ConfigOnly = &cli.BoolFlag{
	Name:    "config-only",
	Aliases: []string{"config"},
	Usage:   "only the configuration repository",
}

var CodeOnly = &cli.BoolFlag{
	Name:    "code-only",
	Aliases: []string{"code"},
	Usage:   "only the code repository",
}

func ParseConfigCodeFlags(ctx *cli.Context) (config bool, code bool, err error) {
	config = ctx.Bool(ConfigOnly.Name)
	code = ctx.Bool(CodeOnly.Name)

	// Cannot clone only code and only config
	if config && code {
		return false, false, projectI18n.BothFlagsCannotBeTrue(ConfigOnly.Name, CodeOnly.Name)
	}

	// Neither only option is selected so both are true
	if !config && !code {
		return true, true, nil
	}

	return config, code, nil
}
