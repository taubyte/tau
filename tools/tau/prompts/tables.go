package prompts

import (
	"os"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/mattn/go-runewidth"
	"github.com/olekukonko/ts"
)

// Using 20 to factor in whitespace and table dividers
const whitespace = 20
const trailingperiod = 3

func RenderTable(data [][]string) {
	t := table.NewWriter()
	size, _ := ts.GetSize()
	termSize := size.Col()
	t.SetOutputMirror(os.Stdout)

	for _, item := range data {
		width := runewidth.StringWidth(item[1]) + runewidth.StringWidth(item[0]) + whitespace
		if width > termSize && termSize != 0 {
			width_to_length := width / (len(item[1]) + len(item[0]))
			desired_length := termSize/width_to_length - len(item[0]) - whitespace - trailingperiod
			item[1] = item[1][:desired_length] + "..."
		}

		t.AppendRow(table.Row{strings.Replace(item[0], "\t", " -  ", 1), item[1]})
		t.AppendSeparator()
	}

	t.SetStyle(table.StyleLight)
	t.SetAllowedRowLength(79)
	t.Render()
}

func RenderTableWithMerge(data [][]string) {
	whitespace := 20
	trailingperiod := 3
	rowConfigAutoMerge := table.RowConfig{AutoMerge: true}
	t := table.NewWriter()
	size, _ := ts.GetSize()
	termSize := size.Col()

	t.SetOutputMirror(os.Stdout)
	t.SetColumnConfigs([]table.ColumnConfig{
		{Number: 1, AutoMerge: true},
	})

	for _, item := range data {
		width := runewidth.StringWidth(item[1]) + runewidth.StringWidth(item[0]) + whitespace
		if width > termSize && termSize != 0 {
			width_to_length := width / (len(item[1]) + len(item[0]))
			desired_length := termSize/width_to_length - len(item[0]) - whitespace - trailingperiod
			item[1] = item[1][:desired_length] + "..."
		}

		t.AppendRow(table.Row{strings.Replace(item[0], "\t", " -  ", 1), item[1]}, rowConfigAutoMerge)
		t.AppendSeparator()
	}

	t.SetStyle(table.StyleLight)
	t.Render()
}
