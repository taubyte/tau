package gen

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"text/template"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var tmpl = template.Must(template.ParseFS(templateFS, "templates/*.tmpl"))

// files is the set of generated file names per resource (each has a matching
// <name>.tmpl). getter_struct/struct/types/pretty stay hand-written.
var files = []string{"set.go", "getter.go", "methods.go", "open.go", "yaml.go"}

func render(file string, r *Resource) ([]byte, error) {
	return exec(file+".tmpl", r, r.Package+"/"+file)
}

func renderStruct(m *StructModel) ([]byte, error) {
	return exec("structs.go.tmpl", m, "structs/"+m.Spec)
}

func exec(name string, data any, label string) ([]byte, error) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return nil, fmt.Errorf("render %s: %w", label, err)
	}
	out, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gofmt %s: %w\n---\n%s", label, err, buf.String())
	}
	return out, nil
}
