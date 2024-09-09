package functionFlags

import (
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
)

// Type (http, p2p, pubsub)
// Timeout
// Memory
// Memory-unit

// (http)
// Method
// Domains
// Paths

// (p2p)
// Protocol, select a service (protocol)
// Command

// (pubsub)
// Channel

// (p2p & pubsub)
// Local

// Source
// Call

var Type = &cli.StringFlag{
	Name:  "type",
	Usage: flags.UsageOneOfOption(common.FunctionTypes),
}
