// Package gen projects the tcc schema DSL (pkg/tcc/taubyte/v2/schema) into the
// files the rest of the tree reads it through: the JSON Schema, the explorer's
// TypeScript, and the typed readers under pkg/specs. It writes them to an output
// dir and can report which on-disk copies have drifted.
package gen

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// The files this tool generates, by repo-relative path.
const (
	tsGenPath      = "pkg/tcc/taubyte/v2/explorer/src/gen/schema.ts"
	jsonSchemaPath = "pkg/tcc/taubyte/v2/config.schema.json"

	// clientTSPath is the same TypeScript, committed again in @taubyte/tcc,
	// which publishes it to authors. A second copy is a second file to drift.
	clientTSPath = "pkg/tcc/clients/js/src/gen/schema.ts"

	// readersPath is the typed view of every kind, as it sits in a compiled
	// object. It is generated for the reason the TS and the JSON Schema are:
	// the paths are the vocabulary's, and a hand-written copy of them is a copy.
	//
	// NOT UNDER pkg/tcc/taubyte/v2. That tree is where the vocabulary is
	// DEFINED, and generated output living inside its own definition makes the
	// two impossible to tell apart — which is the failure this generation
	// exists to end. Generated specs have their own home.
	readersPath = "pkg/specs/kinds.go"
	corePath    = "pkg/specs/project.go"
	readersPkg  = "specs"
)

// Generate returns a map of repo-relative path -> generated file content: the
// TypeScript schema, the JSON schema and the specs readers.
func Generate(root []*engine.Node) (map[string][]byte, error) {
	out := make(map[string][]byte, 2)
	ts, err := GenerateTS(schema.CompileRoot())
	if err != nil {
		return nil, err
	}
	out[filepath.FromSlash(tsGenPath)] = ts
	out[filepath.FromSlash(clientTSPath)] = ts
	js, err := schema.JSONSchema()
	if err != nil {
		return nil, err
	}
	out[filepath.FromSlash(jsonSchemaPath)] = js

	var kinds []string
	for _, g := range root {
		if name, _ := g.Match.(string); name != "" && len(g.Children) > 0 {
			kinds = append(kinds, name)
		}
	}
	out[filepath.FromSlash(readersPath)] = RenderReaders(readersPkg, root, kinds)

	core, err := RenderCore(readersPkg)
	if err != nil {
		return nil, err
	}
	out[filepath.FromSlash(corePath)] = core
	return out, nil
}

// WriteTo writes the generated files under dir, mirroring the repo layout.
func WriteTo(dir string, gen map[string][]byte) error {
	for rel, b := range gen {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Check reports which generated files' on-disk copies are not what the
// vocabulary now says they should be, by repo-relative path.
//
// Every file is generated whole and adopted whole, so "the bytes differ" is the
// entire question.
func Check(repoRoot string, gen map[string][]byte) ([]string, error) {
	var drifted []string
	for rel, b := range gen {
		realBytes, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			if os.IsNotExist(err) {
				drifted = append(drifted, rel)
				continue
			}
			return nil, err
		}
		if !bytes.Equal(b, realBytes) {
			drifted = append(drifted, rel)
		}
	}
	sort.Strings(drifted)
	return drifted, nil
}

// PrintReport prints a per-file OK / drift summary.
func PrintReport(w io.Writer, gen map[string][]byte, drifted []string) {
	drift := make(map[string]bool, len(drifted))
	for _, rel := range drifted {
		drift[rel] = true
	}
	rels := make([]string, 0, len(gen))
	for rel := range gen {
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	for _, rel := range rels {
		if !drift[rel] {
			fmt.Fprintf(w, "OK    %s\n", rel)
			continue
		}
		fmt.Fprintf(w, "DIFF  %s\n", rel)
		fmt.Fprintf(w, "        ! regenerated output differs (run tcc-gen and adopt)\n")
	}
	fmt.Fprintf(w, "\n%d generated files checked, %d with differences\n", len(rels), len(drifted))
}
