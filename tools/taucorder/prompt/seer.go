package prompt

import (
	"errors"
	"fmt"
	"os"

	"github.com/jedib0t/go-pretty/v6/table"

	goPrompt "github.com/c-bata/go-prompt"
	list "github.com/taubyte/tau/tools/taucorder/helpers"
)

var seerTree = &tctree{
	leafs: []*leaf{
		exitLeaf,
		{
			validator: stringValidator("list"),
			ret: []goPrompt.Suggest{
				{
					Text:        "list",
					Description: "show all registered node ids",
				},
			},
			handler: listUsage,
		},
		{
			validator: stringValidator("get"),
			ret: []goPrompt.Suggest{
				{
					Text:        "get",
					Description: "get usage data for a node",
				},
			},
			handler: getUsage,
		},
		{
			validator: stringValidator("listServiceId"),
			ret: []goPrompt.Suggest{
				{
					Text:        "listServiceId",
					Description: "show all registered ids in seers sql for a specific service",
				},
			},
			handler: listServiceId,
		},
	},
}

func listUsage(p Prompt, args []string) error {
	ids, err := p.SeerClient().Usage().List()
	if err != nil {
		return fmt.Errorf("failed listing usage ids with error: %w", err)
	}

	if len(ids) == 0 {
		fmt.Println("No usages are currently stored")
		return nil
	}

	list.CreateTableIds(ids, "Node Id's")

	return nil
}

func listServiceId(p Prompt, args []string) error {
	if len(args) < 2 {
		fmt.Println("Must provide service name")
		return errors.New("must provide service name")
	}

	serviceIds, err := p.SeerClient().Usage().ListServiceId(args[1])
	if err != nil {
		return fmt.Errorf("failed listing usage ids with error: %w", err)
	}

	if len(serviceIds) == 0 {
		return fmt.Errorf("currently no entries for %s", args[1])
	}

	title := fmt.Sprintf("%s Id's", args[1])
	list.CreateTableIds(serviceIds, title)

	return nil
}

func getUsage(p Prompt, args []string) error {
	t := table.NewWriter()
	t.SetStyle(table.StyleLight)
	t.SetOutputMirror(os.Stdout)
	if len(args) < 2 {
		fmt.Println("Must provide node ID")
		return errors.New("must provide node ID")
	}
	id := args[1]
	usg, err := p.SeerClient().Usage().Get(id)
	freemem := usg.FreeMem / 1073741824
	totalmem := usg.TotalMem / 1073741824
	usedmem := usg.UsedMem / 1048576
	if err != nil {
		t.AppendRows([]table.Row{{"--"}},
			table.RowConfig{})
		return fmt.Errorf("failed listing jobs cids with error: %w", err)
	}

	t.AppendRows([]table.Row{{"Id", usg.Id}},
		table.RowConfig{})
	t.AppendSeparator()
	t.AppendRows([]table.Row{{"Name", usg.Name}},
		table.RowConfig{})
	t.AppendSeparator()
	t.AppendRows([]table.Row{{"Type", usg.Type}},
		table.RowConfig{})
	t.AppendSeparator()
	t.AppendRows([]table.Row{{"Timestamp", usg.Timestamp}},
		table.RowConfig{})
	t.AppendSeparator()
	t.AppendRows([]table.Row{{"Address", usg.Address}},
		table.RowConfig{})
	t.AppendSeparator()
	t.AppendRows([]table.Row{{"Memory:\n"}},
		table.RowConfig{})
	t.AppendRows([]table.Row{{"\tMemory Total", fmt.Sprintf("%d GB", totalmem)},
		{"\tMemory Free", fmt.Sprintf("%d GB", freemem)},
		{"\tMemory Used", fmt.Sprintf("%d MB", usedmem)}},
		table.RowConfig{})
	t.AppendSeparator()
	t.AppendRows([]table.Row{{"CPU:\n"}},
		table.RowConfig{})
	t.AppendRows([]table.Row{{"\tCPU Threads", usg.CpuCount},
		{"\tCPU Usage", usg.TotalCpu},
		{"\tCPU User", usg.CpuUser},
		{"\tCPU Idle", usg.CpuIdle}},
		table.RowConfig{})
	t.AppendSeparator()
	t.Render()
	return nil
}
