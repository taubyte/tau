package status

import (
	"fmt"
	"io"
	"os"
	"slices"
	"sort"

	"github.com/jedib0t/go-pretty/v6/table"
	client "github.com/taubyte/tau/clients/http/dream"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/api"
	"github.com/taubyte/tau/tools/dream/cli/command"
	"github.com/urfave/cli/v2"
)

func universe(multiverse *client.Client) *cli.Command {
	c := &cli.Command{
		Name:    "universe",
		Usage:   "List every node of a universe with its ports",
		Aliases: []string{"u"},
		Action:  universeStatus(multiverse),
	}
	command.NameWithDefault(c, dream.DefaultUniverseName)

	return c
}

func universeStatus(multiverse *client.Client) cli.ActionFunc {
	return func(c *cli.Context) (err error) {
		chart, err := multiverse.Universe(c.String("name")).Chart()
		if err != nil {
			return
		}
		renderNodes(os.Stdout, c.String("name"), chart)
		return
	}
}

// known ports first, in this order; any other key follows alphabetically
var portOrder = []string{"http", "p2p", "dns", "ipfs", "copies"}

// renderNodes prints one row per node, sorted, one column per port kind.
func renderNodes(w io.Writer, universe string, chart api.Echart) {
	nodes := chart.Nodes
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })

	var extra []string
	for _, n := range nodes {
		for k := range n.Value {
			if !slices.Contains(portOrder, k) && !slices.Contains(extra, k) {
				extra = append(extra, k)
			}
		}
	}
	sort.Strings(extra)
	cols := append(slices.Clone(portOrder), extra...)

	fmt.Fprintf(w, "Universe %s: %d nodes\n\n", universe, len(nodes))
	t := table.NewWriter()
	t.SetOutputMirror(w)
	header := table.Row{"NODE"}
	for _, col := range cols {
		header = append(header, col)
	}
	t.AppendHeader(header)
	for _, n := range nodes {
		row := table.Row{n.Name}
		for _, col := range cols {
			if v, ok := n.Value[col]; ok {
				row = append(row, fmt.Sprint(v))
			} else {
				row = append(row, "-")
			}
		}
		t.AppendRow(row)
	}
	t.SetStyle(table.StyleLight)
	t.Render()
}
