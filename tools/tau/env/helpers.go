package env

import (
	"fmt"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
)

func justDisplayExport(c *cli.Context, key, value string) bool {
	if c.Bool(flags.Env.Name) {
		fmt.Printf("export %s=%s\n", key, value)
		return true
	}

	return false
}
