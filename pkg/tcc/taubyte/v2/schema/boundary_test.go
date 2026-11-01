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
