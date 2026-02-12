package prompts

import (
	"os"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/mattn/go-runewidth"
	"github.com/olekukonko/ts"
	"github.com/taubyte/tau/tools/tau/output"
)

// Using 20 to factor in whitespace and table dividers
const whitespace = 20
const trailingperiod = 3

func truncateValue(item []string, termSize int, ws int, tp int) {
	contentLen := len(item[1]) + len(item[0])
	if contentLen == 0 {
		return
	}
	width := runewidth.StringWidth(item[1]) + runewidth.StringWidth(item[0]) + ws
	if width > termSize && termSize != 0 {
		widthToLength := width / contentLen
		if widthToLength == 0 {
			return
		}
		desiredLength := termSize/widthToLength - len(item[0]) - ws - tp
		if desiredLength < 0 {
			desiredLength = 0
		}
		if desiredLength < len(item[1]) {
			item[1] = item[1][:desiredLength] + "..."
		}
	}
}

func RenderTable(data [][]string) {
	if output.RenderKeyValue(data) {
		return
	}
	t := table.NewWriter()
	size, _ := ts.GetSize()
	termSize := size.Col()
	t.SetOutputMirror(os.Stdout)

	for _, item := range data {
		if len(item) < 2 {
			continue
		}
		truncateValue(item, termSize, whitespace, trailingperiod)

		t.AppendRow(table.Row{strings.Replace(item[0], "\t", " -  ", 1), item[1]})
		t.AppendSeparator()
	}

	t.SetStyle(table.StyleLight)
	t.SetAllowedRowLength(79)
	t.Render()
}

func RenderTableWithMerge(data [][]string) {
	if output.RenderKeyValue(data) {
		return
	}
	rowConfigAutoMerge := table.RowConfig{AutoMerge: true}
	t := table.NewWriter()
	size, _ := ts.GetSize()
	termSize := size.Col()

	t.SetOutputMirror(os.Stdout)
	t.SetColumnConfigs([]table.ColumnConfig{
		{Number: 1, AutoMerge: true},
	})

	for _, item := range data {
		if len(item) < 2 {
			continue
		}
		truncateValue(item, termSize, whitespace, trailingperiod)

		t.AppendRow(table.Row{strings.Replace(item[0], "\t", " -  ", 1), item[1]}, rowConfigAutoMerge)
		t.AppendSeparator()
	}

	t.SetStyle(table.StyleLight)
	t.Render()
}
