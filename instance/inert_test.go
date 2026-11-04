package instance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A config is inert data: it round-trips through yaml and constructs nothing, or every
// consumer of the type needs a live resource. This reads the package source, since a
// declared method is the problem whether or not it is called.

// inert is what a method on a config type may hand back: the operator's own
// data, and an answer about it. Anything else is a resource.
var inert = map[string]bool{
	"error":         true,
	"bool":          true,
	"string":        true,
	"int":           true,
	"int64":         true,
	"[]string":      true,
	"time.Duration": true,
	"Config":        true,
}

func TestAConfigConstructsNothing(t *testing.T) {
	fset := token.NewFileSet()

	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			receiver := typeName(fn.Recv.List[0].Type)
			if !strings.HasSuffix(receiver, "Config") {
				continue
			}
			checked++

			if fn.Type.Results == nil {
				continue
			}
			for _, result := range fn.Type.Results.List {
				got := typeName(result.Type)
				require.True(t, inert[got],
					"%s: (%s).%s returns %s.\n\n"+
						"A config type may hand back the operator's own data and answers about it, "+
						"never a live resource. Make it a function that TAKES the config — see containerTier.",
					fset.Position(fn.Pos()), receiver, fn.Name.Name, got)
			}
		}
	}

	// THE CENSUS HAS TO HAVE SEEN SOMETHING. A rule checked over an empty set
	// passes forever, including after the thing it guards has been renamed out
	// from under it.
	require.Greater(t, checked, 0, "no methods on a config type were found; has the naming changed?")
}

// typeName renders enough of an expression to compare it.
func typeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeName(t.X)
	case *ast.SelectorExpr:
		return typeName(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + typeName(t.Elt)
	case *ast.MapType:
		return "map[" + typeName(t.Key) + "]" + typeName(t.Value)
	case *ast.Ellipsis:
		return "..." + typeName(t.Elt)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.FuncType:
		return "func"
	}
	return "?"
}
