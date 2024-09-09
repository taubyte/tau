package args

import (
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
)

/*
Gets a string slice from cli.flags so that they can be moved to their required
position from anywhere.
This way `tau new --e` and `tau --e new` are both valid commands.
*/
func ParseFlags(flags []cli.Flag) []ParsedFlag {
	valid := make([]ParsedFlag, len(flags))
	for idx, flag := range flags {
		valid[idx] = ParseFlag(flag)
	}

	return valid
}

func ParseFlag(flag cli.Flag) ParsedFlag {
	names := make([]string, 0)
	for _, name := range flag.Names() {
		names = append(names,
			"-"+name,
			"--"+name,
		)
	}

	_, isBoolFlag := flag.(*cli.BoolFlag)

	if !isBoolFlag {
		_, isBoolFlag = flag.(*flags.BoolWithInverseFlag)
	}

	return ParsedFlag{names, isBoolFlag}
}
