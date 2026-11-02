package codec

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/protogen"
)

var generatedConstructor = regexp.MustCompile(`^New[A-Za-z]+Service(Handler|Client)$`)

// A handler or client built without the codec still works, on protobuf-go's
// reflection, so nothing else would notice one. Every call to a generated
// constructor outside tests must pass an option naming the codec.
func TestEveryConnectSiteTakesTheCodec(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := protogen.FindRepoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}

	sites := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "gen", "testdata", ".deps", "ee", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !generatedConstructor.MatchString(sel.Sel.Name) {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || !strings.HasSuffix(pkg.Name, "connect") {
				return true
			}
			sites++
			for _, arg := range call.Args {
				if strings.Contains(strings.ToLower(string(src[fset.Position(arg.Pos()).Offset:fset.Position(arg.End()).Offset])), "codec") {
					return true
				}
			}
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s:%d: %s.%s is built without the codec", rel, fset.Position(call.Pos()).Line, sel.X, sel.Sel.Name)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if sites == 0 {
		t.Fatal("found no connect construction sites; the walk is not looking where they are")
	}
}
