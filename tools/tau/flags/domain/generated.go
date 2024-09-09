package domainFlags

import (
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
)

var (
	Generated = &flags.BoolWithInverseFlag{
		BoolFlag: &cli.BoolFlag{
			Name:    "generated-fqdn",
			Aliases: []string{"g-fqdn"},
			Usage:   "Generate an FQDN based on the project ID",
		},
	}

	GeneratedPrefix = &cli.StringFlag{
		Name:    "generated-fqdn-prefix",
		Aliases: []string{"g-prefix"},
		Usage:   "Prefix to use when generating an FQDN (Ex: `prefix`-<generated>)",
	}
)
