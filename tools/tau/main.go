// Command tau is the developer CLI for a tau cloud: create and validate a
// project with the v2 schema, link it to a cloud, and watch what it builds.
package main

import (
	"os"

	"github.com/taubyte/tau/tools/tau/internal/cli"
)

func main() { os.Exit(cli.Run(cli.System(), os.Args[1:])) }
