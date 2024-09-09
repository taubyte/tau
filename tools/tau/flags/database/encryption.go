package databaseFlags

import (
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/i18n"
	"github.com/urfave/cli/v2"
)

var Encryption = &flags.BoolWithInverseFlag{
	BoolFlag: &cli.BoolFlag{
		Name:  "encryption",
		Usage: i18n.NotImplemented,
	},
}

var EncryptionKey = &cli.StringFlag{
	Name:    "encryption-key",
	Aliases: []string{"k", "key"},
	Usage:   i18n.NotImplemented,
}
