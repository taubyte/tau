package generic

import (
	"fmt"
	"os"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/taubyte/tau/tools/tau/output"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/urfave/cli/v2"
)

// rows are a resource's applicable fields, in DSL order, as label/value pairs.
func (l link) rows(name string, doc tcc.Doc, showID bool) [][]string {
	var out [][]string
	for _, f := range l.form.Fields {
		if f.Widget == tcc.WidgetCID {
			if showID {
				out = append(out, []string{f.Label, display(tcc.Get(doc, f.Path))})
			}
			continue
		}
		if f.Key == "name" {
			out = append(out, []string{f.Label, name})
			continue
		}
		if !l.form.Visible(f, doc) {
			continue
		}
		if f.IsSelector {
			out = append(out, []string{f.Label, tcc.ActiveBranch(doc, f)})
			continue
		}
		out = append(out, []string{f.Label, display(tcc.Get(doc, tcc.WritePath(doc, f)))})
	}
	return out
}

func display(v any) string {
	if v == nil {
		return ""
	}
	if list := stringList(v); list != nil {
		return strings.Join(list, ", ")
	}
	return fmt.Sprint(v)
}

func (l link) confirm(ctx *cli.Context, prompt, name string, doc tcc.Doc) bool {
	return prompts.ConfirmData(ctx, prompt, l.rows(name, doc, false))
}

// listTable shows one row per resource: its id, name, and the first field the
// DSL puts in the identity section beyond the common ones — enough to tell
// resources apart without naming any resource kind here.
func (l link) listTable(names []string, docs []tcc.Doc) {
	if output.Render(rendered(names, docs)) {
		return
	}
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetAllowedRowLength(79)
	t.AppendHeader(table.Row{"ID", "Name"})
	for i, name := range names {
		id, _ := tcc.Get(docs[i], []string{"id"}).(string)
		if len(id) >= 12 {
			id = id[:6] + "..." + id[len(id)-6:]
		}
		t.AppendRow(table.Row{id, name})
		t.AppendSeparator()
	}
	t.SetStyle(table.StyleLight)
	t.Render()
}

// rendered is the machine-readable form: the document as authored plus its name.
func rendered(names []string, docs []tcc.Doc) []map[string]any {
	out := make([]map[string]any, len(names))
	for i, name := range names {
		d := map[string]any{"name": name}
		for k, v := range docs[i] {
			d[k] = v
		}
		out[i] = d
	}
	return out
}
