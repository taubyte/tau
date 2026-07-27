package cli

import "github.com/urfave/cli/v2"

// extraCommands holds commands contributed by a build-tagged registration seam
// (see register_ee.go). The community build contributes none, so this stays
// empty and the CLI is unchanged.
var extraCommands []*cli.Command
