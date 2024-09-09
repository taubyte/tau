package libraryFlags

import (
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
)

var GenerateRepo = &flags.BoolWithInverseFlag{
	BoolFlag: &cli.BoolFlag{
		Name:    flags.GenerateRepo.Name,
		Aliases: flags.GenerateRepo.Aliases,
		Usage:   flags.GeneratedRepoUsage(common.LibraryRepoPrefix),
	},
}
