package schema_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// V2 DOES NOT IMPORT v1's ADDRESS SPACE, and this is what keeps that true.
//
// v1's `legacy/specs/` is v1's answer to where everything lives: path variables, TNS key
// builders, the resource structs, the parser that reads a key back. It is 98 files and
// most of the platform imports it. None of it is v2's, and v2 states its own — the
// segment constants at the top of index.go say so in as many words, and they are the
// only mention of it anywhere under this tree.
//
// The rule is AGENTS.md §8c — no consumer hardcodes the vocabulary — and v1's legacy/specs is
// that defect at scale: a second copy of an address space the schema already declares,
// which drifts the first time one of them changes. `pkg/schema/` is the same thing one
// layer up: v1's generated accessors, frozen with v1.
//
// It passes vacuously today. That is the point of writing it today: the tree is clean
// exactly once, at the beginning, and this is cheaper than finding out in six months
// which of forty commits reached for a constant that was right there.
//
// If you are here because this failed: what you wanted is either something v2 declares
// (LayoutOf, References, Kinds, EnvironmentsOf, the wasm shapes surface), something v2
// should declare beside its other segments, or something that dies with v1. It is not
// an import.
func TestV2ImportsNoneOfV1sAddressSpace(t *testing.T) {
	const (
		v1specs  = "github.com/taubyte/tau/legacy/specs"
		v1schema = "github.com/taubyte/tau/legacy/schema"
	)
	root, err := filepath.Abs("..")
	assert.NilError(t, err)

	fset := token.NewFileSet()
	var found []string
	assert.NilError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			return perr
		}
		rel, _ := filepath.Rel(root, path)
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if p == v1specs || strings.HasPrefix(p, v1specs+"/") ||
				p == v1schema || strings.HasPrefix(p, v1schema+"/") {
				found = append(found, rel+" imports "+p)
			}
		}
		return nil
	}))

	assert.DeepEqual(t, found, []string(nil))
}
