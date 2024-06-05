package status

import (
	"fmt"
	"os"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	client "github.com/taubyte/tau/clients/http/dream"
	"github.com/taubyte/tau/dream"
	specs "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/tools/dream/cli/command"
	"github.com/urfave/cli/v2"
)

func service(multiverse *client.Client) []*cli.Command {
	validServices := specs.Services
	commands := make([]*cli.Command, len(validServices))

	for idx, _service := range validServices {
		c := &cli.Command{
			Name:   _service,
			Action: runService(_service, multiverse),
		}
		command.Universe0(c)
		commands[idx] = c
	}

	return commands
}
func runService(name string, multiverse *client.Client) cli.ActionFunc {
	return func(c *cli.Context) (err error) {
		chart, err := multiverse.Universe(c.String("universe")).Status()
		if err != nil {
			return
		}

		rowConfigAutoMerge := table.RowConfig{AutoMerge: true}
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, AutoMerge: true},
		})

		var http, secure int
		var found bool
		for _, cat := range chart.Nodes {
			if found {
				break
			}
			for protocol, port := range cat.Value {
				if strings.Contains(cat.Name, name) {
					switch protocol {
					case "http":
						http = port
					case "secure":
						secure = port
					}
					t.AppendRow(table.Row{cat.Name, protocol, port}, rowConfigAutoMerge)
					t.AppendSeparator()
					found = true
				}
			}
		}
		if !found {
			return fmt.Errorf("failed getting service name '%s'", name)
		}
		t.SetStyle(table.StyleLight)

		// Display link
		if http != 0 {
			protocol := "http"
			if secure == 1 {
				protocol = "https"
			}
			fmt.Printf("\n@ %s://%s:%d\n\n", protocol, dream.DefaultHost, http)
		}

		t.Render()

		return
	}
}
