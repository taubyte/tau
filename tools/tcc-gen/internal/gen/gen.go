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

	// readersPath is the typed view of every kind, as it sits in a compiled
	// object. It is generated for the reason the TS and the JSON Schema are:
	// the paths are the vocabulary's, and a hand-written copy of them is a copy.
	//
	// NOT UNDER pkg/tcc/taubyte/v2. That tree is where the vocabulary is
	// DEFINED, and generated output living inside its own definition makes the
	// two impossible to tell apart — which is the failure this generation
	// exists to end. Generated specs have their own home: v1's was
	// legacy/specs, and this is v2's.
	readersPath = "pkg/specs/kinds.go"
	corePath    = "pkg/specs/project.go"
	readersPkg  = "specs"
)

// Generate returns a map of repo-relative path -> generated file content: the
// TypeScript schema and the JSON schema.
//
// IT NO LONGER EMITS GO ACCESSORS. It used to also render legacy/schema/<kind>
// accessor files and legacy/specs/structure struct proposals, which existed to
// project the V1 vocabulary into the v1 services' hand-written packages. v1 is
// not a target and its vocabulary is deleted, so there is nothing to project
// into: generating v2 shapes into legacy/ paths would produce files describing
// a vocabulary those packages do not speak, and the drift check would report
// the difference forever as though it were a mistake somebody could fix.
func Generate(root []*engine.Node) (map[string][]byte, error) {
	out := make(map[string][]byte, 2)
	ts, err := GenerateTS(schema.CompileRoot())
	if err != nil {
		return nil, err
	}
	out[filepath.FromSlash(tsGenPath)] = ts
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
// BYTES, AND NOTHING FINER. It used to also compare sets of declared function
// names, for the Go accessor files this tool projected into the v1 packages:
// those were adopted and then hand-edited, so a name-level check was the only
// honest one. Every file generated now is generated whole and adopted whole,
// which makes "the bytes differ" the entire question — and a path the entire
// answer.
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
