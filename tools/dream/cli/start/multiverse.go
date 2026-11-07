package start

import (
	"os"
	"path/filepath"
	"time"

	"github.com/pterm/pterm"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/api"
	"github.com/taubyte/tau/dream/mcp"
	spec "github.com/taubyte/tau/pkg/specs/common"
	vmLogger "github.com/taubyte/tau/pkg/vm/logger"
	"github.com/taubyte/tau/services/substrate/runtime"
	"github.com/urfave/cli/v2"
)

func runMultiverse() cli.ActionFunc {
	return func(c *cli.Context) (err error) {
		if c.Bool("debug") {
			runtime.DebugFunctionCallsLogger, err = vmLogger.New(os.TempDir())
			if err != nil {
				return err
			}
		}

		// TODO this is ugly, and we should be able to start a universe on a specific branch
		spec.DefaultBranches = []string{c.String("branch")}

		if c.Bool("public") {
			dream.DefaultHost = "0.0.0.0"
		}

		name := c.Args().First()

		if c.Bool("fresh") {
			if err := resetAll(name); err != nil {
				return err
			}
		}

		multiverse, err := dream.New(c.Context, dream.LoadPersistent(), dream.Name(name))
		if err != nil {
			return err
		}
		defer multiverse.Close()

		// Create new API service
		apiService, err := api.New(multiverse, nil)
		if err != nil {
			return err
		}

		// Create and start MCP service
		_, err = mcp.New(multiverse, apiService.Server())
		if err != nil {
			return err
		}

		apiService.Server().Start()

		ready, err := apiService.Ready(10 * time.Second)
		if err != nil || !ready {
			return err
		}

		pterm.Success.Printf("Dream started with Multiverse `%s`\n", multiverse.Name())

		<-c.Done()

		err = multiverse.Close()

		if err != nil {
			pterm.Error.Printf("Dream stopped with error: %s\n", err.Error())
		}

		pterm.Success.Println("Dream stopped")

		return nil
	}
}

// resetAll deletes every kept universe of the multiverse.
func resetAll(multiverse string) error {
	if multiverse == "" {
		multiverse = dream.DefaultMultiverseName
	}

	cacheFolder, err := dream.GetCacheFolder(multiverse)
	if err != nil {
		return err
	}

	roots, err := filepath.Glob(filepath.Join(cacheFolder, "universe-*"))
	if err != nil {
		return err
	}

	for _, root := range roots {
		if err := os.RemoveAll(root); err != nil {
			return err
		}
	}

	return nil
}
