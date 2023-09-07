package app

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/taubyte/tau/cli/node"
	"github.com/taubyte/tau/config"
	"github.com/urfave/cli/v2"
)

// Function startCommand() defines a CLI command named "start"
// It returns a pointer to a cli.Command struct that defines the behavior of this command
func startCommand() *cli.Command {
	return &cli.Command{
		Name:        "start",
		Description: "start a shape",
		// defines the command-line flags
		// can be used with the "start" command
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "shape",
				Required: true,
				Aliases:  []string{"s"},
			},
			&cli.PathFlag{
				Name:  "root",
				Value: config.DefaultRoot,
			},
			&cli.BoolFlag{
				Name:    "dev-mode",
				Aliases: []string{"dev"},
			},
		},

		// Action function that will be exected 
		// when the "start" command is invoked
		// It taks a "cli.Context" as a parameter, 
		// which provides access to the command-line argument and flags
		Action: func(ctx *cli.Context) error {
			// retrieves the value of the "shape" flag 
			// from the command-line arguments and assigns it to the shape variable.
			shape := ctx.String("shape")
			_, protocolConfig, sourceConfig, err := parseSourceConfig(ctx, shape)

			// If an error occurred, returns an error message containing the error.
			if err != nil {
				return fmt.Errorf("parsing config failed with: %s", err)
			}

			// Migration Start
			if _, err := os.Stat(fmt.Sprintf("/tb/storage/databases/%s", shape)); !os.IsNotExist(err) {
				err = migrateDatabase(ctx.Context, shape, len(protocolConfig.Protocols) == 0)
				if err != nil {
					return fmt.Errorf("migrating shape %s failed with: %w", shape, err)
				}
			}

			cmd := exec.Command("sudo", "systemctl", "stop", fmt.Sprintf("odo@%s.service", shape))
			cmd.CombinedOutput()

			cmd = exec.Command("sudo", "systemctl", "disable", fmt.Sprintf("odo@%s.service", shape))
			cmd.CombinedOutput()
			// Migration End

			setNetworkDomains(sourceConfig)
			return node.Start(ctx.Context, protocolConfig)
		},
	}
}
