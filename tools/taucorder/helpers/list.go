package helpers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/jedib0t/go-pretty/v6/table"
)

func CreateTableIds(ids []string, title string) {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{title})
	t.SetStyle(table.StyleLight)
	t.SetColumnConfigs([]table.ColumnConfig{
		{
			Number:    1,
			AutoMerge: true,
		},
	})
	for _, _ids := range ids {
		if ids == nil {
			t.AppendRows([]table.Row{{"--"}},
				table.RowConfig{})
		} else {
			t.AppendRows([]table.Row{{_ids}},
				table.RowConfig{})
		}
		t.AppendSeparator()
	}
	t.Render()
}

func CreateTableInterface(title string, iface interface{}) error {
	js := recursiveToJSON(iface)
	marshalled, err := json.Marshal(js)
	if err != nil {
		return fmt.Errorf("Failed marshall with %v", err)
	}

	var buf bytes.Buffer
	err = json.Indent(&buf, marshalled, "", "    ")
	if err != nil {
		return fmt.Errorf("Failed json indent with %v", err)
	}

	_, err = io.Copy(os.Stdout, &buf)
	fmt.Println()

	return err
}
