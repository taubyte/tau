package schema_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// Follows a consumer through pkg/specs' generated types (`specs.ReadGateway(r)`
// then `gw.Route.Path`). A named field is a key read, with the key taken off the
// generated reader. A value is a generated type when a ReadX produced it, it was
// declared as one, or it was reached through one; not a type checker, but a
// missed read surfaces as an unread attribute.

// genField is one field of a generated type: a leaf at a key, or a nested type
// read under a prefix.
type genField struct {
	leaf   []string // the key a scalar is read at, relative to its reader's resource
	sub    string   // the generated type a block or an entry reads as
	prefix []string // what the nested reader is handed, relative to this one's
}

// genTypes is every generated struct and its fields, off pkg/specs/kinds.go.
type genTypes map[string]map[string]genField

var (
	generatedOnce sync.Once
	generated     genTypes
)

func generatedTypes(t *testing.T, root string) genTypes {
	generatedOnce.Do(func() {
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "pkg/specs/kinds.go"), nil, 0)
		require.NoError(t, err)
		generated = readGenerated(file)
	})
	require.NotEmpty(t, generated, "pkg/specs/kinds.go declares no generated readers")
	return generated
}

func readGenerated(file *ast.File) genTypes {
	out := genTypes{}
	for _, declared := range file.Decls {
		function, ok := declared.(*ast.FuncDecl)
		if !ok || function.Recv != nil || function.Body == nil || !strings.HasPrefix(function.Name.Name, "Read") {
			continue
		}
		name := strings.TrimPrefix(function.Name.Name, "Read")
		fields := map[string]genField{}
		lists := map[string][]string{}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if loop, ok := node.(*ast.RangeStmt); ok {
				if call, ok := loop.X.(*ast.CallExpr); ok {
					if value, ok := loop.Value.(*ast.Ident); ok {
						lists[value.Name] = append(literalPath(call), "*")
					}
				}
			}
			assign, ok := node.(*ast.AssignStmt)
			if !ok || len(assign.Lhs) == 0 {
				return true
			}
			target := assign.Lhs[0]
			if index, ok := target.(*ast.IndexExpr); ok {
				target = index.X
			}
			field, ok := target.(*ast.SelectorExpr)
			if !ok || field.Sel.Name == "Declared" {
				return true
			}
			for _, right := range assign.Rhs {
				ast.Inspect(right, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					if callee, ok := call.Fun.(*ast.Ident); ok && strings.HasPrefix(callee.Name, "Read") && len(call.Args) == 1 {
						var prefix []string
						switch arg := call.Args[0].(type) {
						case *ast.CallExpr:
							prefix = pathsOf(arg, nil, nil)[0]
						case *ast.Ident:
							prefix = lists[arg.Name]
						}
						fields[field.Sel.Name] = genField{sub: strings.TrimPrefix(callee.Name, "Read"), prefix: prefix}
						return false
					}
					method, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || method.Sel.Name == "Has" {
						return true
					}
					if _, held := fields[field.Sel.Name]; !held {
						fields[field.Sel.Name] = genField{leaf: pathsOf(call, nil, nil)[0]}
					}
					return false
				})
			}
			return true
		})
		out[name] = fields
	}
	return out
}

func literalPath(call *ast.CallExpr) []string {
	var out []string
	for _, arg := range call.Args {
		if text, ok := stringLit(arg); ok {
			out = append(out, text)
		}
	}
	return out
}

// bound is what a name or an expression is: a generated type, read under a
// prefix of the resource it came from.
type bound struct {
	typ    string
	prefix []string
}

// typedPackage is what a consumer's own package declares in generated types:
// struct fields and function results, so `c.gateway.Route` and `gatewayOf(r).Route`
// are followed into a file that never called a reader itself.
type typedPackage struct {
	fields  map[string]string
	results map[string]string
}

var (
	typedPackagesMu sync.Mutex
	typedPackages   = map[string]typedPackage{}
)

func typedPackageOf(dir string, types genTypes) typedPackage {
	typedPackagesMu.Lock()
	defer typedPackagesMu.Unlock()
	if held, ok := typedPackages[dir]; ok {
		return held
	}
	out := typedPackage{fields: map[string]string{}, results: map[string]string{}}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			continue
		}
		alias := specsAlias(file)
		if alias == "" {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.StructType:
				for _, field := range value.Fields.List {
					if typ := typeName(field.Type, alias, types); typ != "" {
						for _, name := range field.Names {
							out.fields[name.Name] = typ
						}
					}
				}
			case *ast.FuncDecl:
				if value.Type.Results != nil && len(value.Type.Results.List) > 0 {
					if typ := typeName(value.Type.Results.List[0].Type, alias, types); typ != "" {
						out.results[value.Name.Name] = typ
					}
				}
			}
			return true
		})
	}
	typedPackages[dir] = out
	return out
}

// specsAlias is the name a file imports pkg/specs under, or "" when it does not.
func specsAlias(file *ast.File) string {
	for _, imported := range file.Imports {
		if imported.Path.Value != specsImportPath {
			continue
		}
		if imported.Name != nil {
			return imported.Name.Name
		}
		return "specs"
	}
	return ""
}

// typeName is the generated type a declared type holds, one or many of: a
// collection of entries is read the way one entry is.
func typeName(e ast.Expr, alias string, types genTypes) string {
	switch value := e.(type) {
	case *ast.SelectorExpr:
		if pkg, ok := value.X.(*ast.Ident); ok && pkg.Name == alias {
			if _, ok := types[value.Sel.Name]; ok {
				return value.Sel.Name
			}
		}
	case *ast.StarExpr:
		return typeName(value.X, alias, types)
	case *ast.ArrayType:
		return typeName(value.Elt, alias, types)
	case *ast.MapType:
		return typeName(value.Value, alias, types)
	case *ast.Ellipsis:
		return typeName(value.Elt, alias, types)
	}
	return ""
}

// typedReadsIn is every key one file reads through a generated type, as the
// patterns readsIn produces for a spelled read.
func typedReadsIn(file *ast.File, pkg typedPackage, types genTypes) map[string]bool {
	out := map[string]bool{}
	alias := specsAlias(file)
	if alias == "" {
		return out
	}

	for _, declared := range file.Decls {
		function, ok := declared.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		env := map[string]bound{}
		params := func(list *ast.FieldList) {
			if list == nil {
				return
			}
			for _, field := range list.List {
				if typ := typeName(field.Type, alias, types); typ != "" {
					for _, name := range field.Names {
						env[name.Name] = bound{typ: typ}
					}
				}
			}
		}
		params(function.Type.Params)

		var typeOf func(e ast.Expr) (bound, bool)
		typeOf = func(e ast.Expr) (bound, bool) {
			switch value := e.(type) {
			case *ast.Ident:
				held, ok := env[value.Name]
				return held, ok
			case *ast.ParenExpr:
				return typeOf(value.X)
			case *ast.StarExpr:
				return typeOf(value.X)
			case *ast.UnaryExpr:
				return typeOf(value.X)
			case *ast.IndexExpr:
				return typeOf(value.X)
			case *ast.CallExpr:
				switch fun := value.Fun.(type) {
				case *ast.SelectorExpr:
					if pkgName, ok := fun.X.(*ast.Ident); ok && pkgName.Name == alias && strings.HasPrefix(fun.Sel.Name, "Read") {
						if _, ok := types[strings.TrimPrefix(fun.Sel.Name, "Read")]; ok {
							return bound{typ: strings.TrimPrefix(fun.Sel.Name, "Read")}, true
						}
					}
					if typ, ok := pkg.results[fun.Sel.Name]; ok {
						return bound{typ: typ}, true
					}
				case *ast.Ident:
					if typ, ok := pkg.results[fun.Name]; ok {
						return bound{typ: typ}, true
					}
				}
			case *ast.SelectorExpr:
				if outer, ok := typeOf(value.X); ok {
					if field, ok := types[outer.typ][value.Sel.Name]; ok && field.sub != "" {
						return bound{typ: field.sub, prefix: append(append([]string{}, outer.prefix...), field.prefix...)}, true
					}
					return bound{}, false
				}
				if typ, ok := pkg.fields[value.Sel.Name]; ok {
					return bound{typ: typ}, true
				}
			}
			return bound{}, false
		}

		bind := func(target ast.Expr, value ast.Expr) {
			name, ok := target.(*ast.Ident)
			if !ok || name.Name == "_" {
				return
			}
			if held, ok := typeOf(value); ok {
				env[name.Name] = held
			}
		}

		// Twice, so a name bound after a closure that reads it is still known.
		for round := 0; round < 2; round++ {
			ast.Inspect(function.Body, func(node ast.Node) bool {
				switch value := node.(type) {
				case *ast.FuncLit:
					params(value.Type.Params)
				case *ast.AssignStmt:
					if len(value.Lhs) == len(value.Rhs) {
						for i := range value.Lhs {
							bind(value.Lhs[i], value.Rhs[i])
						}
					} else if len(value.Rhs) == 1 {
						bind(value.Lhs[0], value.Rhs[0])
					}
				case *ast.ValueSpec:
					if typ := typeName(value.Type, alias, types); typ != "" {
						for _, name := range value.Names {
							env[name.Name] = bound{typ: typ}
						}
					}
					for i := range value.Values {
						if i < len(value.Names) {
							bind(value.Names[i], value.Values[i])
						}
					}
				case *ast.RangeStmt:
					if value.Value != nil {
						bind(value.Value, value.X)
					}
				}
				return true
			})
		}

		ast.Inspect(function.Body, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			outer, ok := typeOf(selector.X)
			if !ok {
				return true
			}
			// A PRESENCE TEST IS NOT A VALUE, here as in the generated reader:
			// Declared says a block was written and reads none of its fields.
			if field, ok := types[outer.typ][selector.Sel.Name]; ok && field.leaf != nil {
				out[strings.Join(append(append([]string{}, outer.prefix...), field.leaf...), "/")] = true
			}
			return true
		})
	}
	return out
}

// absoluteKeys is every compiled key a kind's generated reader decodes, as a
// pattern from the resource: what a spelled read is measured against.
func absoluteKeys(types genTypes, typ string, prefix []string, depth int, out map[string]bool) {
	if depth > 8 {
		return
	}
	for _, field := range types[typ] {
		if field.leaf != nil {
			out[strings.Join(append(append([]string{}, prefix...), field.leaf...), "/")] = true
			continue
		}
		absoluteKeys(types, field.sub, append(append([]string{}, prefix...), field.prefix...), depth+1, out)
	}
}
