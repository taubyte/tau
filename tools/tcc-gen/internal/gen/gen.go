// Package gen projects the tcc schema DSL (pkg/tcc/taubyte/v1/schema) into the
// mechanical pkg/schema/<resource> accessor files. It writes to an output dir
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
)

// schemaPrefix scopes the accessor drift check (a formatting-agnostic func-name
// diff). structPrefix scopes the structureSpec struct files, which are fully
// generated and adopted in place, so they get a strict byte-exact check.
const (
	schemaPrefix = "pkg/schema/"
	structPrefix = "pkg/specs/structure/"
	tsGenPath    = "pkg/tcc/clients/js/src/gen/schema.ts"
)

// Generate returns a map of repo-relative path -> gofmt'd file content: the
// pkg/schema accessor files plus the pkg/specs/structure struct proposals.
func Generate(root []*engine.Node) (map[string][]byte, error) {
	rs, err := Resources(root)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(rs)*len(files))
	for _, r := range rs {
		for _, f := range files {
			b, err := render(f, r)
			if err != nil {
				return nil, err
			}
			out[filepath.Join("pkg", "schema", r.Package, f)] = b
		}
	}
	structs, err := Structs(root)
	if err != nil {
		return nil, err
	}
	for _, m := range structs {
		b, err := renderStruct(m)
		if err != nil {
			return nil, err
		}
		out[filepath.Join("pkg", "specs", "structure", strings.ToLower(m.Spec)+".go")] = b
	}
	ts, err := GenerateTS(root)
	if err != nil {
		return nil, err
	}
	out[filepath.FromSlash(tsGenPath)] = ts
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
		case strings.HasPrefix(rel, structPrefix) || rel == filepath.FromSlash(tsGenPath):
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
		if strings.HasPrefix(rel, schemaPrefix) || strings.HasPrefix(rel, structPrefix) || rel == filepath.FromSlash(tsGenPath) {
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
