package scripts

import (
	"fmt"
	"go/ast"
	"go/parser"
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
//	holds a lock, a once, a wait group, a sync.Map, a sync.Cond or an atomic
//	is built by a constructor (new*, New*) that is not a known value builder
//	is a pointer to a composite literal that is not an error
//	is written anywhere but its declaration and its package's init, tests
//	included: assigned, incremented, ranged into, deleted from, cleared, copied
//	into, or has its address taken
//
// and cannot see state a function builds behind a pointer it returns under any
// other name, nor a method that changes what a var holds. A var that has to be
// one of these says why in allowed.
func TestNothingIsAPackageLevelSingleton(t *testing.T) {
	found, err := findGlobals("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found.problems {
		if why, ok := allowed[f.key]; ok {
			if why == "" {
				t.Errorf("%s is allowed with no reason", f.key)
			}
			continue
		}
		t.Error(f)
	}
	for k := range allowed {
		if !found.declared[k] {
			t.Errorf("allowed names %s, which is no longer declared", k)
		}
	}
}

// Every way the guard knows to hold or write state is caught in the fixture, and
// nothing it lets through is.
func TestTheGuardCatchesWhatItSays(t *testing.T) {
	found, err := findGlobals(filepath.Join("testdata", "globals"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range found.problems {
		got = append(got, f.key+" "+f.rule)
	}
	slices.Sort(got)
	want := []string{
		"a.Counter written",
		"a.addr written",
		"a.cleared written",
		"a.copied written",
		"a.deleted written",
		"a.gate holds a singleton's state",
		"a.hits holds a singleton's state",
		"a.inc written",
		"a.kept holds a singleton's state",
		"a.lock holds a singleton's state",
		"a.made is built by a constructor",
		"a.ranged written",
		"a.shared points to a composite literal",
		"a.state holds a singleton's state",
		"a.swapped written",
		"a.wg holds a singleton's state",
		"latest.mu holds a singleton's state",
	}
	if !slices.Equal(got, want) {
		t.Errorf("the guard found\n\t%s\nwant\n\t%s", strings.Join(got, "\n\t"), strings.Join(want, "\n\t"))
	}
}

// allowed is each package-level var that has to be state, and why.
var allowed = map[string]string{
	"pkg/containers/backends/containerd.treeOnce": "the process's own cgroup, which preparing moves the process into: once per process",
	"pkg/containers/backends/containerd.tree":     "the process's own cgroup, which preparing moves the process into: once per process",
	"pkg/containers/backends/containerd.treeErr":  "the process's own cgroup, which preparing moves the process into: once per process",
	"pkg/kvdb.contextGens":                        "a source of unique numbers, shared only in being unique",
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

// syncTypes are the sync package's types that mark a singleton; sync.Pool and the
// Once* funcs are not among them.
var syncTypes = []string{"Mutex", "RWMutex", "Once", "WaitGroup", "Map", "Cond"}

type problem struct {
	key, at, rule, detail string
}

func (p problem) String() string { return fmt.Sprintf("%s: %s %s: %s", p.at, p.key, p.rule, p.detail) }

type found struct {
	problems []problem
	declared map[string]bool
}

type parsed struct {
	dir, rel string
	file     *ast.File
	test     bool
	// imports are this file's tau imports by name; sync and atomic are what it
	// calls those two packages.
	imports      map[string]string
	sync, atomic string
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

const module = "github.com/taubyte/tau/"

func findGlobals(root string) (found, error) {
	fset := token.NewFileSet()
	var files []*parsed
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
		files = append(files, &parsed{dir: filepath.Dir(rel), rel: rel, file: f, test: strings.HasSuffix(p, "_test.go")})
		return nil
	})
	if err != nil {
		return found{}, err
	}

	pkgName := map[string]string{}
	for _, f := range files {
		if !f.test {
			pkgName[f.dir] = f.file.Name.Name
		}
	}
	for _, f := range files {
		f.imports = map[string]string{}
		for _, im := range f.file.Imports {
			path := strings.Trim(im.Path.Value, `"`)
			name := filepath.Base(path)
			if im.Name != nil {
				name = im.Name.Name
			}
			switch {
			case path == "sync":
				f.sync = name
			case path == "sync/atomic":
				f.atomic = name
			case strings.HasPrefix(path, module):
				dir := filepath.FromSlash(strings.TrimPrefix(path, module))
				if n, ok := pkgName[dir]; ok && im.Name == nil {
					name = n
				}
				f.imports[name] = dir
			}
		}
	}

	out := found{declared: map[string]bool{}}
	byDir := map[string]map[string]bool{}
	topLevel := map[any]bool{}
	key := func(dir, name string) string { return filepath.ToSlash(dir) + "." + name }
	for _, f := range files {
		if f.test || isTestSupport(f.dir) {
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
					if byDir[f.dir] == nil {
						byDir[f.dir] = map[string]bool{}
					}
					byDir[f.dir][n.Name] = true
					k := key(f.dir, n.Name)
					out.declared[k] = true
					at := fmt.Sprintf("%s:%d", filepath.ToSlash(f.rel), fset.Position(n.Pos()).Line)
					if rule := breaks(f, vs.Type, e); rule != "" {
						out.problems = append(out.problems, problem{key: k, at: at, rule: rule, detail: describe(e, vs.Type)})
					}
				}
			}
		}
	}

	for _, f := range files {
		for _, decl := range f.file.Decls {
			// A package's own init composes it: what it writes there is its
			// declaration, finished.
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "init" && fn.Recv == nil && !f.test {
				continue
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				for _, e := range written(n) {
					dir, name, ok := target(e, f, byDir[f.dir], topLevel)
					if !ok {
						continue
					}
					if dir == "" {
						dir = f.dir
					}
					if !byDir[dir][name] {
						continue
					}
					out.problems = append(out.problems, problem{
						key: key(dir, name), rule: "written",
						at:     fmt.Sprintf("%s:%d", filepath.ToSlash(f.rel), fset.Position(e.Pos()).Line),
						detail: "a package-level var written after it is declared",
					})
				}
				return true
			})
		}
	}
	slices.SortFunc(out.problems, func(a, b problem) int { return strings.Compare(a.String(), b.String()) })
	return out, nil
}

// written is what a statement or call writes: the left of an assignment, what is
// incremented or ranged into, the first argument of delete, clear and copy, and
// anything whose address is taken.
func written(n ast.Node) []ast.Expr {
	switch s := n.(type) {
	case *ast.AssignStmt:
		if s.Tok != token.DEFINE {
			return s.Lhs
		}
	case *ast.IncDecStmt:
		return []ast.Expr{s.X}
	case *ast.RangeStmt:
		if s.Tok == token.ASSIGN {
			return slices.DeleteFunc([]ast.Expr{s.Key, s.Value}, func(e ast.Expr) bool { return e == nil })
		}
	case *ast.CallExpr:
		if id, ok := s.Fun.(*ast.Ident); ok && id.Obj == nil && len(s.Args) > 0 {
			switch id.Name {
			case "delete", "clear", "copy":
				return s.Args[:1]
			}
		}
	case *ast.UnaryExpr:
		if s.Op == token.AND {
			return []ast.Expr{s.X}
		}
	}
	return nil
}

// testSupport is the packages a test imports and no binary does, and what sits
// under them. A name ending in "test" is not enough: latest is a word.
var testSupport = []string{"p2p/peer/peertest", "pkg/forge/forgetest", "pkg/git/gittest", "services/cm/cmtest"}

func isTestSupport(dir string) bool {
	dir = filepath.ToSlash(dir)
	for _, ts := range testSupport {
		if dir == ts || strings.HasPrefix(dir, ts+"/") {
			return true
		}
	}
	return false
}

func breaks(f *parsed, typ, e ast.Expr) string {
	switch {
	case holdsSync(f, typ) || (e != nil && holdsSync(f, e) && !isOnceValue(f, e)):
		return "holds a singleton's state"
	case isConstructed(e):
		return "is built by a constructor"
	case isPointerLiteral(e):
		return "points to a composite literal"
	}
	return ""
}

// holdsSync reports a sync type or an atomic anywhere in the expression, however
// the file names the two packages.
func holdsSync(f *parsed, e ast.Expr) bool {
	if e == nil {
		return false
	}
	held := false
	ast.Inspect(e, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return !held
		}
		if x, ok := sel.X.(*ast.Ident); ok {
			if (f.sync != "" && x.Name == f.sync && slices.Contains(syncTypes, sel.Sel.Name)) || (f.atomic != "" && x.Name == f.atomic) {
				held = true
			}
		}
		return !held
	})
	return held
}

// isOnceValue is sync.OnceValue, OnceValues or OnceFunc of a func: a value derived
// once, which is fine.
func isOnceValue(f *parsed, e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	fun := call.Fun
	switch x := fun.(type) {
	case *ast.IndexExpr:
		fun = x.X
	case *ast.IndexListExpr:
		fun = x.X
	}
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && f.sync != "" && x.Name == f.sync && slices.Contains([]string{"OnceValue", "OnceValues", "OnceFunc"}, sel.Sel.Name)
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
	case *ast.IndexListExpr:
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

// target is the package-level var an expression writes, through any element or
// field: dir is empty for the file's own package.
func target(e ast.Expr, f *parsed, local map[string]bool, topLevel map[any]bool) (dir, name string, ok bool) {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
			continue
		case *ast.IndexExpr:
			e = x.X
			continue
		case *ast.IndexListExpr:
			e = x.X
			continue
		case *ast.StarExpr:
			e = x.X
			continue
		case *ast.SliceExpr:
			e = x.X
			continue
		case *ast.SelectorExpr:
			if id, isIdent := x.X.(*ast.Ident); isIdent && id.Obj == nil {
				if d, imported := f.imports[id.Name]; imported {
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

func describe(e, typ ast.Expr) string {
	if e == nil {
		e = typ
	}
	if e == nil {
		return ""
	}
	return nodeText(e)
}

// nodeText is enough of an expression to say which it is.
func nodeText(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.CallExpr:
		return callee(x.Fun) + "(...)"
	case *ast.UnaryExpr:
		return x.Op.String() + nodeText(x.X)
	case *ast.CompositeLit:
		return callee(x.Type) + "{...}"
	case *ast.SelectorExpr, *ast.Ident, *ast.IndexExpr:
		return callee(x)
	case *ast.StructType:
		return "struct{...}"
	}
	return fmt.Sprintf("%T", e)
}
