package scripts

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// NOTHING IS A SINGLETON OR SHARED STATE THAT CAN RACE. A package-level var is a
// value every user of the package in a process shares, so state lives with what
// owns it: a node keeps what its packages share (peer.Kept), a service its own
// tunables, a test its own network. Immutable values are fine: sentinels,
// constants, compiled patterns, descriptors, loggers, pools, lazily derived
// values (sync.OnceValue).
//
// What this reads is syntax, so it fails a var that
//
//	holds a lock, a once, a wait group, a sync.Map or an atomic: a singleton's mark
//	is assigned anywhere but its declaration and its package's init, tests included
//	is built by a constructor (new*, New*) that is not a known value builder
//	is a pointer to a composite literal
//
// and cannot see state a function builds behind a pointer it returns under any
// other name. A var that has to be one of these says why in allowed.
func TestNothingIsAPackageLevelSingleton(t *testing.T) {
	g := readGlobals(t, "..")
	var bad []string
	for _, v := range g.vars {
		if why, ok := allowed[v.pkg+"."+v.name]; ok {
			if why == "" {
				t.Errorf("%s.%s is allowed with no reason", v.pkg, v.name)
			}
			continue
		}
		if rule := v.breaks(); rule != "" {
			bad = append(bad, fmt.Sprintf("%s: %s %s", v.at, v.name, rule))
		}
	}
	for _, w := range g.writes {
		if _, ok := allowed[w.pkg+"."+w.name]; !ok {
			bad = append(bad, fmt.Sprintf("%s: assigns %s.%s, a package-level var", w.at, w.pkg, w.name))
		}
	}
	slices.Sort(bad)
	for _, b := range bad {
		t.Error(b)
	}
	for k := range allowed {
		if !g.declared[k] {
			t.Errorf("allowed names %s, which is no longer declared", k)
		}
	}
}

// allowed is each package-level var that has to be state, and why.
var allowed = map[string]string{
	"pkg/containers/backends/containerd.treeOnce": "the process's own cgroup, which preparing moves the process into: once per process",
	"pkg/containers/backends/containerd.tree":     "the process's own cgroup, which preparing moves the process into: once per process",
	"pkg/containers/backends/containerd.treeErr":  "the process's own cgroup, which preparing moves the process into: once per process",
	"pkg/kvdb.contextGens":                        "a source of unique numbers, shared only in being unique",
	"pkg/kvdb.replicas":                           "interns the replica ids every key parse names, the same strings whichever store asks; a table per store would be threaded through every parse on the hot path",
	"pkg/tcc/taubyte/v2/wasm.sessions":            "the JS bridge's handles: JS calls exported functions, which have no receiver to hold them",
	"pkg/tcc/taubyte/v2/wasm.nextHandle":          "the JS bridge's handles: JS calls exported functions, which have no receiver to hold them",
	"pkg/forge/bitbucket.routesMu":                "routes enrol as the package's vars initialise, and are read-only once they have",
	"pkg/forge/bitbucket.routeTable":              "routes enrol as the package's vars initialise, and are read-only once they have",
	"pkg/forge/bitbucket.enrolled":                "routes enrol as the package's vars initialise, and are read-only once they have",
	"pkg/forge/gitea.routesMu":                    "routes enrol as the package's vars initialise, and are read-only once they have",
	"pkg/forge/gitea.routeTable":                  "routes enrol as the package's vars initialise, and are read-only once they have",
	"pkg/forge/gitea.enrolled":                    "routes enrol as the package's vars initialise, and are read-only once they have",
	"pkg/forge/github.routesMu":                   "routes enrol as the package's vars initialise, and are read-only once they have",
	"pkg/forge/github.routeTable":                 "routes enrol as the package's vars initialise, and are read-only once they have",
	"pkg/forge/github.enrolled":                   "routes enrol as the package's vars initialise, and are read-only once they have",
	"pkg/forge/gitlab.routesMu":                   "routes enrol as the package's vars initialise, and are read-only once they have",
	"pkg/forge/gitlab.routeTable":                 "routes enrol as the package's vars initialise, and are read-only once they have",
	"pkg/forge/gitlab.enrolled":                   "routes enrol as the package's vars initialise, and are read-only once they have",
	"pkg/forge/gogs.routesMu":                     "routes enrol as the package's vars initialise, and are read-only once they have",
	"pkg/forge/gogs.routeTable":                   "routes enrol as the package's vars initialise, and are read-only once they have",
	"pkg/forge/gogs.enrolled":                     "routes enrol as the package's vars initialise, and are read-only once they have",
	"pkg/forge/onedev.routesMu":                   "routes enrol as the package's vars initialise, and are read-only once they have",
	"pkg/forge/onedev.routeTable":                 "routes enrol as the package's vars initialise, and are read-only once they have",
	"pkg/forge/onedev.enrolled":                   "routes enrol as the package's vars initialise, and are read-only once they have",
}

// valueBuilders are constructors whose result is a value nothing changes.
var valueBuilders = []string{"errors.New", "connect.NewError", "strings.NewReplacer", "base32.NewEncoding", "cron.NewParser", "lipgloss.NewStyle", "newRoute"}

// syncState is what marks a var as a singleton's state.
var syncState = []string{"sync.Mutex", "sync.RWMutex", "sync.Once", "sync.WaitGroup", "sync.Map", "sync.Cond", "atomic."}

type global struct {
	pkg, name, at, init string
	expr                ast.Expr
}

type write struct {
	pkg, name, at string
}

type globals struct {
	vars     []global
	writes   []write
	declared map[string]bool
}

func (v global) breaks() string {
	switch {
	case containsAny(v.init, syncState) && !strings.Contains(v.init, "sync.OnceValue") && !strings.Contains(v.init, "sync.OnceFunc"):
		return "holds a singleton's state: " + v.init
	case isConstructed(v.expr):
		return "is built by a constructor: " + v.init
	case isPointerLiteral(v.expr):
		return "points to a composite literal: " + v.init
	}
	return ""
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// isConstructed reports a call to new* or New* that is no value builder, however
// it is chained.
func isConstructed(e ast.Expr) bool {
	for {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return false
		}
		name := callee(call.Fun)
		base := name[strings.LastIndex(name, ".")+1:]
		if strings.HasPrefix(base, "new") || strings.HasPrefix(base, "New") {
			return !slices.Contains(valueBuilders, name)
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		e = sel.X
	}
}

func callee(e ast.Expr) string {
	switch f := e.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok {
			return x.Name + "." + f.Sel.Name
		}
		return f.Sel.Name
	case *ast.IndexExpr:
		return callee(f.X)
	}
	return ""
}

// isPointerLiteral reports &T{...}, unless T is an error type, which is how a
// sentinel of a struct error type is spelled.
func isPointerLiteral(e ast.Expr) bool {
	u, ok := e.(*ast.UnaryExpr)
	if !ok || u.Op != token.AND {
		return false
	}
	lit, ok := u.X.(*ast.CompositeLit)
	return ok && !strings.HasSuffix(callee(lit.Type), "Error")
}

// testSupport is a package a test imports, never a binary: peertest, gittest,
// and what sits under forgetest.
func testSupport(dir, name string) bool {
	if strings.HasSuffix(name, "test") {
		return true
	}
	for _, part := range strings.Split(dir, string(filepath.Separator)) {
		if strings.HasSuffix(part, "test") {
			return true
		}
	}
	return false
}

type parsed struct {
	dir, rel, path string
	file           *ast.File
	test           bool
}

// skipped is what is not tau's own code: other modules' trees, guest programs,
// examples, and what a test reads.
func skipped(rel string) bool {
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "ee" || part == "vendor" || part == "testdata" || part == "node_modules" || part == "fixtures" || part == "agent" ||
			(strings.HasPrefix(part, ".") && part != ".") || (strings.HasPrefix(part, "_") && len(part) > 1) {
			return true
		}
	}
	return false
}

func readGlobals(t *testing.T, root string) globals {
	t.Helper()
	fset := token.NewFileSet()
	var files []parsed
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			if rel != "." && skipped(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		if ast.IsGenerated(f) {
			return nil
		}
		files = append(files, parsed{dir: filepath.Dir(rel), rel: rel, path: p, file: f, test: strings.HasSuffix(p, "_test.go")})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	g := globals{declared: map[string]bool{}}
	byDir := map[string]map[string]bool{}
	topLevel := map[any]bool{}
	pkgName := map[string]string{}
	for _, f := range files {
		if f.test {
			continue
		}
		pkgName[f.dir] = f.file.Name.Name
		if testSupport(f.dir, f.file.Name.Name) {
			continue
		}
		for _, decl := range f.file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				topLevel[vs] = true
				for i, n := range vs.Names {
					if n.Name == "_" {
						continue
					}
					var e ast.Expr
					if i < len(vs.Values) {
						e = vs.Values[i]
					}
					text := describe(fset, e, vs.Type)
					if byDir[f.dir] == nil {
						byDir[f.dir] = map[string]bool{}
					}
					byDir[f.dir][n.Name] = true
					key := filepath.ToSlash(f.dir) + "." + n.Name
					g.declared[key] = true
					g.vars = append(g.vars, global{
						pkg: filepath.ToSlash(f.dir), name: n.Name, init: text, expr: e,
						at: fmt.Sprintf("%s:%d", filepath.ToSlash(f.rel), fset.Position(n.Pos()).Line),
					})
				}
			}
		}
	}

	const module = "github.com/taubyte/tau/"
	for _, f := range files {
		imports := map[string]string{}
		for _, im := range f.file.Imports {
			path := strings.Trim(im.Path.Value, `"`)
			if !strings.HasPrefix(path, module) {
				continue
			}
			dir := filepath.FromSlash(strings.TrimPrefix(path, module))
			alias := filepath.Base(dir)
			if n, ok := pkgName[dir]; ok {
				alias = n
			}
			if im.Name != nil {
				alias = im.Name.Name
			}
			imports[alias] = dir
		}
		for _, decl := range f.file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			// A package's own init composes it: what it writes there is its
			// declaration, finished.
			if isFunc && fn.Name.Name == "init" && fn.Recv == nil && !f.test {
				continue
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				var lhs []ast.Expr
				switch s := n.(type) {
				case *ast.AssignStmt:
					if s.Tok == token.DEFINE {
						return true
					}
					lhs = s.Lhs
				case *ast.IncDecStmt:
					lhs = []ast.Expr{s.X}
				default:
					return true
				}
				for _, e := range lhs {
					dir, name, ok := target(e, imports, byDir[f.dir], topLevel)
					if !ok {
						continue
					}
					if dir == "" {
						dir = f.dir
					}
					if !byDir[dir][name] {
						continue
					}
					g.writes = append(g.writes, write{
						pkg: filepath.ToSlash(dir), name: name,
						at: fmt.Sprintf("%s:%d", filepath.ToSlash(f.rel), fset.Position(e.Pos()).Line),
					})
				}
				return true
			})
		}
	}
	return g
}

// target is the package-level var an assignment's left side writes, through any
// element or field: dir is empty for the file's own package.
func target(e ast.Expr, imports map[string]string, local map[string]bool, topLevel map[any]bool) (dir, name string, ok bool) {
	for {
		switch x := e.(type) {
		case *ast.IndexExpr:
			e = x.X
			continue
		case *ast.StarExpr:
			e = x.X
			continue
		case *ast.SelectorExpr:
			if id, isIdent := x.X.(*ast.Ident); isIdent && id.Obj == nil {
				if d, imported := imports[id.Name]; imported {
					return d, x.Sel.Name, true
				}
			}
			e = x.X
			continue
		case *ast.Ident:
			if local[x.Name] && (x.Obj == nil || topLevel[x.Obj.Decl]) {
				return "", x.Name, true
			}
		}
		return "", "", false
	}
}

func describe(fset *token.FileSet, e ast.Expr, typ ast.Expr) string {
	var b bytes.Buffer
	switch {
	case e != nil:
		printer.Fprint(&b, fset, e)
	case typ != nil:
		printer.Fprint(&b, fset, typ)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
