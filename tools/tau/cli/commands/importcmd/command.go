// Package importcmd is `tau import`: point it at a file from another platform
// and get a v2 project plus an honest account of what did not come across.
//
// # The report is the product, not the config
//
// Every importer in pkg/importer is loud on purpose, and this is where that
// pays off. A silent import that produces plausible-looking config is the
// dangerous kind: the person who reads it later believes it. So the command
// prints four lists, and the order is deliberate —
//
//	blocked      the source did something tau cannot express
//	missing      a required field the source does not name
//	approximated it carried, and it does not mean the same thing
//	elsewhere    the value is real and is not in this file
//
// A project is written even when it does not compile yet, because a compose
// file names no memory limit and a compose volume names no size, and refusing
// would make the command useless on the first file anybody tries. Whether it
// compiles is printed, so nobody has to guess.
package importcmd

import (
	"github.com/urfave/cli/v2"
)

var (
	fromFlag = &cli.StringFlag{
		Name:    "from",
		Aliases: []string{"f"},
		Usage:   "Source format: compose, vercel, fly, coolify, dokploy, lambda. Inferred from the file when unset.",
	}
	outFlag = &cli.StringFlag{
		Name:    "out",
		Aliases: []string{"o"},
		Usage:   "Directory to write the project into",
		Value:   ".",
	}
	nameFlag = &cli.StringFlag{
		Name:    "name",
		Aliases: []string{"n"},
		Usage:   "Project name. Taken from the file, or the source file's directory, when unset.",
	}
	dryRunFlag = &cli.BoolFlag{
		Name:  "dry-run",
		Usage: "Report what would be imported and write nothing",
	}
)

// Command is `tau import <file>`.
var Command = &cli.Command{
	Name:      "import",
	Usage:     "Import a project from another platform",
	ArgsUsage: "<file>",
	Description: "Reads a docker-compose.yml, vercel.json, fly.toml, or a SAM/Serverless template and writes a\n" +
		"v2 project beside it — with a report of everything that did not carry across.\n\n" +
		"Nothing is invented: a field tau cannot honour is reported rather than emitted, because config\n" +
		"carrying a setting into a system that ignores it is worse than a refusal line.",
	Flags:  []cli.Flag{fromFlag, outFlag, nameFlag, dryRunFlag},
	Action: run,
}
