package app

import (
	"fmt"
	"time"

	"github.com/taubyte/tau/config"
	"github.com/urfave/cli/v2"
)

func newApp() *cli.App {
	// Try to extract version and compilation time from the build information that
	// Go embeds starting from Go 1.18 when modules are enabled. When the binary
	// is built with the default settings (buildvcs in Go 1.22), the commit hash
	// is stored under the setting key "vcs.revision" and the commit timestamp
	// under "vcs.time". If the information is not present (e.g. `go run` or an
	// older compiler), we fall back to "unknown".

	var (
		version  = Version // default value injected via ldflags, or "unknown"
		compiled time.Time
	)

	if BuildDate != "unknown" && BuildDate != "" {
		if t, err := time.Parse(time.RFC3339, BuildDate); err == nil {
			compiled = t
		}
	}

	if version == "unknown" {
		// Fallback to information embedded by the Go toolchain if ldflags were
		// not provided (e.g. during `go run`).
		if info, ok := debugInfo(); ok {
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					version = s.Value
				case "vcs.time":
					// The value is an RFC3339 timestamp ("2024-03-01T12:34:56Z").
					if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
						compiled = t
					}
				}
			}
		}
	}

	app := &cli.App{
		Version:  version,
		Compiled: compiled,
		Flags: []cli.Flag{
			&cli.PathFlag{
				Name:  "root",
				Value: config.DefaultRoot,
				Usage: "Folder where tau is installed",
			},
		},
		Commands: []*cli.Command{
			buildInfoCommand(),
			startCommand(),
			configCommand(),
		},
	}

	// Custom version printer to include commit and build date
	cli.VersionPrinter = func(cCtx *cli.Context) {
		ver := Version
		if ver == "unknown" || ver == "" {
			ver = "n/a"
		}

		com := Commit
		if com == "unknown" || com == "" {
			// if not provided via ldflags, try ReadBuildInfo
			if info, ok := debugInfo(); ok {
				for _, s := range info.Settings {
					if s.Key == "vcs.revision" {
						com = s.Value
						break
					}
				}
			}
			if com == "" || com == "unknown" {
				com = "n/a"
			}
		}

		dateStr := BuildDate
		if dateStr == "unknown" || dateStr == "" {
			if !app.Compiled.IsZero() {
				dateStr = app.Compiled.Format(time.RFC3339)
			} else {
				dateStr = "n/a"
			}
		}

		fmt.Fprintf(cCtx.App.Writer, "version: %s\ncommit: %s\nbuilt at: %s\n", ver, com, dateStr)
	}

	return app
}

func Run(args ...string) error {
	err := newApp().Run(args)
	if err != nil {
		return err
	}

	return nil
}
