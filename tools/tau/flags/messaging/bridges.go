package messagingFlags

import (
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
)

var MQTT = &flags.BoolWithInverseFlag{
	BoolFlag: &cli.BoolFlag{
		Name:  "mqtt",
		Usage: "Enable the MQTT broker feature",
	},
}

var WebSocket = &flags.BoolWithInverseFlag{
	BoolFlag: &cli.BoolFlag{
		Name:    "web-socket",
		Aliases: []string{"ws"},
		Usage:   "Enable joining the pubsub through websocket",
	},
}
