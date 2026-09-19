// Package gen projects the tcc schema DSL (pkg/tcc/taubyte/v2/schema) into the
// mechanical legacy/schema/<resource> accessor files. It writes to an output dir
// for review and can diff its output against the existing hand-written files.
package gen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// schemaPrefix scopes the accessor drift check (a formatting-agnostic func-name
// diff). structPrefix scopes the structureSpec struct files, which are fully
// generated and adopted in place, so they get a strict byte-exact check.
const (
	schemaPrefix   = "legacy/schema/"
	structPrefix   = "legacy/specs/structure/"
	tsGenPath      = "pkg/tcc/taubyte/v2/explorer/src/gen/schema.ts"
	jsonSchemaPath = "pkg/tcc/taubyte/v2/config.schema.json"

	// readersPath is the typed view of every kind, as it sits in a compiled
	// object. It is generated for the reason the TS and the JSON Schema are:
	// the paths are the vocabulary's, and a hand-written copy of them is a copy.
	readersPath = "pkg/project/kinds/kinds.go"
	readersPkg  = "kinds"
)

// byteExact reports whether a generated file is checked byte-for-byte (struct
// files, the TS schema, the JSON schema) rather than by func-name set.
func byteExact(rel string) bool {
	return strings.HasPrefix(rel, structPrefix) ||
		rel == filepath.FromSlash(tsGenPath) ||
		rel == filepath.FromSlash(jsonSchemaPath) ||
		rel == filepath.FromSlash(readersPath)
}

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

// Diff reports how a generated file's set of declared function/method names
// differs from the hand-written counterpart. This is a formatting-agnostic
// semantic check: ExtraInGen names should never appear (that's a generator bug
// or a documented rename); MissingInGen names are the hand-written custom
// helpers/transforms the generator deliberately leaves alone.
type Diff struct {
	Rel          string
	ExtraInGen   []string // generated but absent from the real file
	MissingInGen []string // present in the real file but not generated
	BytesDiffer  bool     // struct files: generated output != on-disk (byte-exact)
}

// Check diffs every generated file against its on-disk counterpart under
// repoRoot: struct files byte-exact, accessor files by func-name set.
func Check(repoRoot string, gen map[string][]byte) ([]Diff, error) {
	var diffs []Diff
	for rel, b := range gen {
		switch {
		case byteExact(rel):
			realBytes, err := os.ReadFile(filepath.Join(repoRoot, rel))
			if err != nil {
				if os.IsNotExist(err) {
					diffs = append(diffs, Diff{Rel: rel, BytesDiffer: true})
					continue
				}
				return nil, err
			}
			if !bytes.Equal(b, realBytes) {
				diffs = append(diffs, Diff{Rel: rel, BytesDiffer: true})
			}
		case strings.HasPrefix(rel, schemaPrefix):
			genNames, err := funcNames(rel, b)
			if err != nil {
				return nil, fmt.Errorf("parse generated %s: %w", rel, err)
			}
			realBytes, err := os.ReadFile(filepath.Join(repoRoot, rel))
			if err != nil {
				if os.IsNotExist(err) {
					diffs = append(diffs, Diff{Rel: rel, ExtraInGen: sortedKeys(genNames)})
					continue
				}
				return nil, err
			}
			realNames, err := funcNames(rel, realBytes)
			if err != nil {
				return nil, fmt.Errorf("parse real %s: %w", rel, err)
			}
			extra := missing(genNames, realNames)
			miss := missing(realNames, genNames)
			if len(extra) > 0 || len(miss) > 0 {
				diffs = append(diffs, Diff{Rel: rel, ExtraInGen: extra, MissingInGen: miss})
			}
		}
	}
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Rel < diffs[j].Rel })
	return diffs, nil
}

// PrintReport prints a per-file OK / drift summary.
func PrintReport(w io.Writer, gen map[string][]byte, diffs []Diff) {
	drift := make(map[string]Diff, len(diffs))
	for _, d := range diffs {
		drift[d.Rel] = d
	}
	rels := make([]string, 0, len(gen))
	for rel := range gen {
		if strings.HasPrefix(rel, schemaPrefix) || byteExact(rel) {
			rels = append(rels, rel)
		}
	}
	sort.Strings(rels)

	for _, rel := range rels {
		d, differs := drift[rel]
		if !differs {
			fmt.Fprintf(w, "OK    %s\n", rel)
			continue
		}
		fmt.Fprintf(w, "DIFF  %s\n", rel)
		if d.BytesDiffer {
			fmt.Fprintf(w, "        ! regenerated output differs (run tcc-gen and adopt)\n")
		}
		if len(d.ExtraInGen) > 0 {
			fmt.Fprintf(w, "        + only in generated: %v\n", d.ExtraInGen)
		}
		if len(d.MissingInGen) > 0 {
			fmt.Fprintf(w, "        - only in hand-written: %v\n", d.MissingInGen)
		}
	}
	fmt.Fprintf(w, "\n%d generated files checked, %d with differences\n", len(rels), len(diffs))
}

// funcNames returns the set of top-level func/method names declared in src.
func funcNames(name string, src []byte) (map[string]bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			names[fn.Name.Name] = true
		}
	}
	return names, nil
}

// missing returns keys in a that are not in b, sorted.
func missing(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
