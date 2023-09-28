package app

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/taubyte/tau/cli/node"
	"github.com/taubyte/tau/config"
	"github.com/urfave/cli/v2"
)

// startCommand returns a new CLI command for starting a shape
func startCommand() *cli.Command {
	return &cli.Command{
		Name:        "start",
		Description: "start a shape",
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

		Action: func(ctx *cli.Context) error {
			shape := ctx.String("shape") // Get the value of the "shape" flag
			_, protocolConfig, sourceConfig, err := parseSourceConfig(ctx, shape) // Parse the source config
			if err != nil {
				return fmt.Errorf("parsing config failed with: %s", err)
			}

			// Migration Start
			// Check if the shape's database exists
			if _, err := os.Stat(fmt.Sprintf("/tb/storage/databases/%s", shape)); !os.IsNotExist(err) {
				// Migrate the shape's database
				err = migrateDatabase(ctx.Context, shape, len(protocolConfig.Protocols) == 0)
				if err != nil {
					return fmt.Errorf("migrating shape %s failed with: %w", shape, err)
				}
			}

			// Stop the systemd service for the shape
			cmd := exec.Command("sudo", "systemctl", "stop", fmt.Sprintf("odo@%s.service", shape))
			cmd.CombinedOutput()

			// Disable the systemd service for the shape
			cmd = exec.Command("sudo", "systemctl", "disable", fmt.Sprintf("odo@%s.service", shape))
			cmd.CombinedOutput()
			// Migration End

			setNetworkDomains(sourceConfig) // Set network domains
			return node.Start(ctx.Context, protocolConfig)  // Start the shape's node
		},
	}
}
