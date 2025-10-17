package start

import (
	"time"

	"github.com/pterm/pterm"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/api"
	"github.com/taubyte/tau/dream/mcp"
	spec "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/services/substrate/runtime"
	"github.com/urfave/cli/v2"
)

func runMultiverse() cli.ActionFunc {
	return func(c *cli.Context) (err error) {
		runtime.DebugFunctionCalls = c.Bool("debug")

		// TODO this is ugly, and we should be able to start a universe on a specific branch
		spec.DefaultBranches = []string{c.String("branch")}

		if c.Bool("public") {
			dream.DefaultHost = "0.0.0.0"
		}

		name := c.Args().First()

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
