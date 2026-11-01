package forgetest

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/forge/forgetest/hermetic"
)

// The inverse guard. Every contract case must be named by a proof, a subject
// wrong in a named way plus the exact complaints the suite makes about it, and
// every fault must be named by a proof. A marker step needs no fault and must
// have none. The population is found by type with go/types, not by spelling,
// and every body is walked: a declaration or a literal, wherever it is bound.
//
// Not covered: a failure that is no testify-shaped call, TB failure method or
// marker (a panic, an os.Exit), and a claim written as a marker's own firing,
// which no rule of shape can close.

type kind int

const (
	contract kind = iota
	plumbing
)

func (k kind) String() string {
	if k == plumbing {
		return "plumbing"
	}
	return "a contract assertion"
}

// assertion is one case: a failing call reached through one chain of call
// sites, since a table-driven assertion is one site and many contracts. A
// case's words are its format with its chain's constants substituted.
type assertion struct {
	id    string // the case: this site, and the chain that reaches it
	site  string // "run.go:729", the way a stack frame spells it
	where string // the call site this case comes through, for a human
	call  string // "assert.Error"
	says  string // what it says HERE, with this chain's constants filled in
	in    string // "capabilities", the function it is written in
	kind  kind
}

// callable is a function, a method, or a closure a function bound to a name:
// anything a case can pass through on its way to an assertion.
type callable struct {
	obj    types.Object
	name   string
	file   string
	body   *ast.BlockStmt
	params []*types.Var
	lit    *ast.FuncLit // set for a named closure, so its parent skips its body
}

type edge struct {
	from *callable
	to   *callable
	call *ast.CallExpr
}

// failing is the set of methods that fail a testing.TB. A receiver can be
// called anything and a package can be imported under any alias, but a method
// that stops a test is one of these six: the name is the interface's, not the
// call site's, and renaming it does not compile.
var failing = map[string]bool{
	"Error": true, "Errorf": true, "Fatal": true, "Fatalf": true,
	"Fail": true, "FailNow": true,
}

// source is this suite's own package, type-checked. Everything the ledger
// reads about the population comes from here.
type source struct {
	pkg      *packages.Package
	reporter *types.Interface // anything with an Errorf: testify's TestingT, tb, *testing.T
	fails    *types.Signature // Errorf's own shape, for a failure whose binding is invisible
	step     types.Type       // the marker's question type

	// The walk, the bindings and the marker firings are computed once per
	// source and read by everything below. They are cached here and not
	// recomputed per question because every rule is read over the same source,
	// and because a walk that answers differently at two call sites is a walk
	// with two definitions of what a body is.
	walkOnce sync.Once
	theWalk  *walk

	boundOnce sync.Once
	bound     map[types.Object][]ast.Expr

	firingOnce sync.Once
	firing     map[*ast.CallExpr]bool

	// faults is read out of the fake forge's source rather than listed: a list is a
	// thing you can forget to add to.
	faults map[string]string

	// rules is every rule name this file DECLARES, read the same way and for
	// the same reason. A rule constant that no registry entry carries is a rule
	// nothing reads, and the registry is a list somebody can shorten.
	rules map[string]string

	// harnesses is every name the three things that RUN the rules are declared
	// under, read the same way again. The rules were closed and the loops that
	// execute them were not, and a loop nothing runs is every rule turned off.
	harnesses map[string]string
}

var (
	loadOnce sync.Once
	loaded   *source
	loadErr  error

	overlaid    sync.Mutex
	overlaidBy  = map[string]*source{}
	overlaidLRU []string
)

// overlayCacheSize bounds memory, not speed: each entry is a full type-check of
// the suite, and an unbounded cache held sixteen, 2.85 GB live. Three covers
// consecutive reuse.
const overlayCacheSize = 3

// suiteSource type-checks the suite's own source, test files excluded, through
// hermetic.Load.
func suiteSource(t *testing.T) *source {
	t.Helper()
	loadOnce.Do(func() { loaded, loadErr = loadSuite(nil) })
	if loadErr != nil {
		t.Fatalf("type-checking the suite's own source, which is the population every rule below is computed over: %v", loadErr)
	}
	return loaded
}

// with is this package with one more file, so a rule about the source gets a
// fault: a violating source it must complain about.
func (s *source) with(t *testing.T, name, content string) *source {
	t.Helper()
	return s.withIn(t, ".", name, content)
}

// withIn is the same, in some other package of this tree. A fault is declared
// in the fake forge and not here, so a fault whose DECLARATION is what is being
// shown has to be put where faults live.
func (s *source) withIn(t *testing.T, dir, name, content string) *source {
	t.Helper()
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("finding the suite's own directory, to show the ledger a source that breaks its rules: %v", err)
	}
	path := filepath.Join(dir, name)
	key := path + "\x00" + content

	overlaid.Lock()
	defer overlaid.Unlock()
	if got, ok := overlaidBy[key]; ok {
		return got
	}
	got, err := loadSuite(map[string][]byte{path: []byte(content)})
	if err != nil {
		t.Fatalf("type-checking the suite's own source with %s added: %v", name, err)
	}
	overlaidBy[key] = got
	overlaidLRU = append(overlaidLRU, key)
	for len(overlaidLRU) > overlayCacheSize {
		delete(overlaidBy, overlaidLRU[0])
		overlaidLRU = overlaidLRU[1:]
	}
	return got
}

func loadSuite(overlay map[string][]byte) (*source, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
		Dir: ".",
		// The test files are type-checked as well, not because they are part of
		// the population — they are the thing doing the asserting — but because
		// the ledger's own rule names are declared in them and are read out of
		// source rather than trusted to a list.
		Tests:   true,
		Overlay: overlay,
	}
	// The fake forge is loaded beside the suite so its faults can be counted.
	// Its import path comes from the type itself and not from a string here.
	faultType := reflect.TypeOf(fake.Fault(""))
	pkgs, err := hermetic.Load(cfg, ".", faultType.PkgPath())
	if err != nil {
		return nil, err
	}
	var pkg, faked, tested *packages.Package
	for _, p := range pkgs {
		switch {
		case p.PkgPath == faultType.PkgPath() && p.ID == p.PkgPath:
			faked = p
		case p.PkgPath != suitePath:
			continue
		case p.ID == p.PkgPath:
			pkg = p // the suite itself, without its test files
		case strings.HasSuffix(p.ID, ".test]"):
			tested = p // the same package WITH them, where the rules are declared
		}
	}
	if pkg == nil || faked == nil || tested == nil {
		return nil, fmt.Errorf("loading the suite, its tests and the fake forge answered %d packages and not the three needed", len(pkgs))
	}
	for _, p := range []*packages.Package{pkg, faked, tested} {
		if len(p.Errors) > 0 {
			return nil, fmt.Errorf("%v", p.Errors)
		}
	}
	if pkg.TypesInfo == nil || len(pkg.Syntax) == 0 {
		return nil, fmt.Errorf("the suite's own package came back with no type information")
	}
	obj := pkg.Types.Scope().Lookup("step")
	if obj == nil {
		return nil, fmt.Errorf("this package declares no `step` type, which is what marks a marker; see plumbing.go. Without it every step reads as a claim")
	}
	// The fake forge's own source is where a fault is CONSULTED, and the
	// suite's is where one is injected: a constant whose line has been stripped
	// of its type is typed at either, so both are read.
	faults, err := constantsOf(faked, faultType.Name(), tested)
	if err != nil {
		return nil, err
	}
	rules, err := constantsOf(tested, reflect.TypeOf(ruleName("")).Name())
	if err != nil {
		return nil, err
	}
	harnesses, err := constantsOf(tested, reflect.TypeOf(harnessName("")).Name())
	if err != nil {
		return nil, err
	}
	report := reporterInterface()
	return &source{pkg: pkg, reporter: report, fails: report.Method(0).Type().(*types.Signature),
		step: obj.Type(), faults: faults, rules: rules, harnesses: harnesses}, nil
}

var suitePath = reflect.TypeOf(assertion{}).PkgPath()

// theForgeTree is every package this suite and the drivers it holds live in,
// taken from suitePath rather than written out.
var theForgeTree = path.Dir(suitePath) + "/..."

// theOneLoader is the function every source load in this tree has to go
// through, and thePin is the one that sets the environment for a whole binary.
// Both are named by the function value and not by a string.
var (
	theOneLoader = runtime.FuncForPC(reflect.ValueOf(hermetic.Load).Pointer()).Name()
	thePin       = runtime.FuncForPC(reflect.ValueOf(hermetic.Pin).Pointer()).Name()
)

// looseLoads is every packages.Load in the forge tree under an environment
// nobody chose: neither through hermetic.Load nor in a binary hermetic.Pin
// pinned. Resolved through go/types, so aliases and bound variables count.
func looseLoads(t *testing.T, overlay map[string][]byte) []string {
	t.Helper()
	mode := packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
		packages.NeedImports | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo
	pkgs, err := treeSource(t, mode, overlay)
	if err != nil {
		t.Fatalf("type-checking the forge tree, which is where every source load in it is written: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatalf("type-checking %s answered no packages, so this closure is over nothing", theForgeTree)
	}
	loaderPkg := theOneLoader[:strings.LastIndex(theOneLoader, ".")]
	pinned := linkersOf(pkgs, pinnersIn(pkgs))
	seen := map[string]bool{}
	var out []string
	for _, p := range pkgs {
		if p.TypesInfo == nil || p.PkgPath == loaderPkg || pinned[p.ID] {
			continue
		}
		for _, file := range p.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				// Every reference, not every call: `var load = packages.Load` is the same load
				// one name away.
				id, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				fn, ok := p.TypesInfo.Uses[id].(*types.Func)
				if !ok || fn.FullName() != "golang.org/x/tools/go/packages.Load" {
					return true
				}
				at := p.Fset.Position(id.Pos())
				// The package a file sits in, because two drivers in this tree
				// both write their route table in a file called route_test.go.
				site := filepath.Base(filepath.Dir(at.Filename)) + "/" +
					filepath.Base(at.Filename) + ":" + strconv.Itoa(at.Line)
				if seen[site] {
					return true
				}
				seen[site] = true
				out = append(out, site)
				return true
			})
		}
	}
	sort.Strings(out)
	return out
}

// pinnersIn is read out of source: a package merely importing the suite pins
// nothing if the call went away.
func pinnersIn(pkgs []*packages.Package) map[string]bool {
	out := map[string]bool{}
	for _, p := range pkgs {
		if p.TypesInfo == nil {
			continue
		}
		for _, file := range p.Syntax {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Name.Name != "init" || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					if f, ok := p.TypesInfo.Uses[sel.Sel].(*types.Func); ok && f.FullName() == thePin {
						out[p.PkgPath] = true
					}
					return true
				})
			}
		}
	}
	return out
}

// linkersOf is every loaded package that reaches one of a set of import paths,
// by ID. The tree's packages are all roots, so the paths named in their Imports
// are enough to walk it — nothing outside this module can import a package
// inside it.
func linkersOf(pkgs []*packages.Package, want map[string]bool) map[string]bool {
	byPath := map[string]*packages.Package{}
	for _, p := range pkgs {
		if p.ID == p.PkgPath {
			byPath[p.PkgPath] = p
		}
	}
	reaches := map[string]bool{}
	var walk func(p *packages.Package, seen map[string]bool) bool
	walk = func(p *packages.Package, seen map[string]bool) bool {
		if p == nil || seen[p.ID] {
			return false
		}
		seen[p.ID] = true
		for path := range p.Imports {
			if want[path] || walk(byPath[path], seen) {
				return true
			}
		}
		return false
	}
	for _, p := range pkgs {
		if want[p.PkgPath] || walk(p, map[string]bool{}) {
			reaches[p.ID] = true
		}
	}
	return reaches
}

// treeSource type-checks every package in the forge tree, once, and once more
// for each overlay a rule's violating ledger asks for.
func treeSource(t *testing.T, mode packages.LoadMode, overlay map[string][]byte) ([]*packages.Package, error) {
	t.Helper()
	if overlay != nil {
		return hermetic.Load(&packages.Config{Mode: mode, Dir: ".", Tests: true, Overlay: overlay}, theForgeTree)
	}
	loadedTree.Do(func() {
		treePkgs, treeErr = hermetic.Load(&packages.Config{Mode: mode, Dir: ".", Tests: true}, theForgeTree)
	})
	return treePkgs, treeErr
}

var (
	loadedTree sync.Once
	treePkgs   []*packages.Package
	treeErr    error
)

// constantsOf is every constant of one named string type in a package, by value
// and name. A constant is also found by its type where it is used, so stripping
// the type from its declaration does not drop it. Types are compared by
// qualified name: a root package and the same package seen through export data
// are not identical.
func constantsOf(decl *packages.Package, typeName string, alsoUsedIn ...*packages.Package) (map[string]string, error) {
	scope := decl.Types.Scope()
	obj := scope.Lookup(typeName)
	if obj == nil {
		return nil, fmt.Errorf("%s declares no %s type, which is what a fault is", decl.PkgPath, typeName)
	}
	want := obj.Type().String()

	out := map[string]string{}
	keep := func(c *types.Const) {
		if c.Pkg() == nil || c.Pkg().Path() != decl.PkgPath || c.Val().Kind() != constant.String {
			return
		}
		out[constant.StringVal(c.Val())] = c.Name()
	}
	for _, name := range scope.Names() {
		if c, ok := scope.Lookup(name).(*types.Const); ok && c.Type().String() == want {
			keep(c)
		}
	}
	for _, pkg := range append([]*packages.Package{decl}, alsoUsedIn...) {
		if pkg == nil || pkg.TypesInfo == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				c, ok := pkg.TypesInfo.Uses[id].(*types.Const)
				if !ok {
					return true
				}
				if tv, ok := pkg.TypesInfo.Types[id]; ok && tv.Type != nil && tv.Type.String() == want {
					keep(c)
				}
				return true
			})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s declares no %s at all", decl.PkgPath, typeName)
	}
	return out, nil
}

// reporterInterface is `interface{ Errorf(string, ...any) }`, built rather than
// imported because it must match three unrelated declarations: testify's
// TestingT, this package's tb, and testing.TB. Anything a call can fail
// through has it.
func reporterInterface() *types.Interface {
	anyT := types.NewInterfaceType(nil, nil)
	anyT.Complete()
	sig := types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewVar(token.NoPos, nil, "format", types.Typ[types.String]),
			types.NewVar(token.NoPos, nil, "args", types.NewSlice(anyT)),
		), nil, true)
	iface := types.NewInterfaceType([]*types.Func{
		types.NewFunc(token.NoPos, nil, "Errorf", sig),
	}, nil)
	iface.Complete()
	return iface
}

type failure struct {
	spelling string // how it reads at the call site, for a message
	at       int    // which argument carries the message, or -1
	kind     kind
}

// howItFails says whether a call is one of the ways this suite fails, where its
// message sits, and which kind it is, by what the call is rather than how it
// reads.
func (s *source) howItFails(call *ast.CallExpr) (failure, bool) {
	// A marker's OWN failure — through the reporter it was handed, reporting
	// the question it was handed — is the marker firing, and the marker is
	// already one entry at each of its call sites. It is the one thing excused
	// from the population, and it is excused by what it says rather than by
	// which function it is written in: a claim written beside it is a claim.
	if s.markerFirings()[call] {
		return failure{}, false
	}
	fn, recv := s.callee(call)
	sig, ok := s.signatureOf(call, fn)
	if !ok {
		return failure{}, false
	}
	spelling := s.spell(call)

	// A marker: any function that asks for a step. The question is where its
	// message is, which is what a step says it was on its way to.
	if at := s.stepParam(sig); at >= 0 {
		return failure{spelling: spelling, at: at, kind: plumbing}, true
	}

	// A method that fails a TB, on a receiver that can be failed through.
	if fn != nil && recv != nil && failing[fn.Name()] && types.Implements(recv, s.reporter) {
		return failure{spelling: spelling, at: messageArg(sig), kind: contract}, true
	}

	// Or the thing being called IS a way to fail the test: the same six
	// methods reached through a name, a field or a value rather than through
	// the receiver they belong to.
	if s.isAWayToFail(call.Fun, 0) {
		return failure{spelling: spelling, at: messageArg(sig), kind: contract}, true
	}

	// A leaf assertion: a body this package does not hold, handed something it
	// can fail through. Handed a REPORTER, which is testify under whatever
	// name; or handed the METHOD that fails one without the reporter it belongs
	// to, which is a call whose signature says nothing about failing anything
	// and whose call site says all of it.
	if fn != nil && fn.Pkg() != nil && fn.Pkg() != s.pkg.Types &&
		(takesAReporter(sig, s.reporter) || s.handedAWayToFail(call)) {
		return failure{spelling: spelling, at: messageArg(sig), kind: contract}, true
	}
	return failure{}, false
}

// callee follows a name bound to a function to that function.
func (s *source) callee(call *ast.CallExpr) (*types.Func, types.Type) {
	info := s.pkg.TypesInfo
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.SelectorExpr:
		if sel, ok := info.Selections[fun]; ok {
			if fn, ok := sel.Obj().(*types.Func); ok {
				return fn, info.TypeOf(fun.X)
			}
			return s.followsTo(sel.Obj(), 0)
		}
		if fn, ok := info.Uses[fun.Sel].(*types.Func); ok {
			return fn, nil
		}
		return s.followsTo(info.Uses[fun.Sel], 0)
	case *ast.Ident:
		if fn, ok := info.Uses[fun].(*types.Func); ok {
			return fn, nil
		}
		return s.followsTo(info.Uses[fun], 0)
	}
	return nil, nil
}

// followsTo is the function a NAME ultimately calls, read out of what this
// package binds that name to.
//
// A name bound to a function LITERAL resolves to nothing here, on purpose: that
// literal is a body in this package and is walked in its own right, so the
// assertion in it is counted where it is written and counting the call as well
// would count it twice.
func (s *source) followsTo(obj types.Object, depth int) (*types.Func, types.Type) {
	v, ok := obj.(*types.Var)
	if !ok || depth > 4 {
		return nil, nil
	}
	info := s.pkg.TypesInfo
	for _, bound := range s.bindings()[v] {
		switch e := ast.Unparen(bound).(type) {
		case *ast.SelectorExpr:
			if sel, ok := info.Selections[e]; ok {
				if fn, ok := sel.Obj().(*types.Func); ok {
					return fn, info.TypeOf(e.X)
				}
				continue
			}
			if fn, ok := info.Uses[e.Sel].(*types.Func); ok {
				return fn, nil
			}
			if fn, recv := s.followsTo(info.Uses[e.Sel], depth+1); fn != nil {
				return fn, recv
			}
		case *ast.Ident:
			if fn, ok := info.Uses[e].(*types.Func); ok {
				return fn, nil
			}
			if fn, recv := s.followsTo(info.Uses[e], depth+1); fn != nil {
				return fn, recv
			}
		}
	}
	return nil, nil
}

// signatureOf is the signature of what a call calls: the resolved function's
// where there is one, and otherwise the type of the expression being called —
// which is what answers for a func-typed field, a map entry or anything else
// whose binding is not visible from here.
func (s *source) signatureOf(call *ast.CallExpr, fn *types.Func) (*types.Signature, bool) {
	if fn != nil {
		if sig, ok := fn.Type().(*types.Signature); ok {
			return sig, true
		}
	}
	t := s.pkg.TypesInfo.TypeOf(ast.Unparen(call.Fun))
	if t == nil {
		return nil, false
	}
	sig, ok := t.Underlying().(*types.Signature)
	return sig, ok
}

// isAWayToFail: a reporter, or one of the six methods that fail one, however
// named or bound. Where the binding is invisible only Errorf's shape answers:
// the other five are shaped like every callback.
func (s *source) isAWayToFail(e ast.Expr, depth int) bool {
	if e == nil || depth > 4 {
		return false
	}
	e = ast.Unparen(e)
	info := s.pkg.TypesInfo

	// A reporter itself: t, tb, testify's TestingT, anything with an Errorf.
	if t := info.TypeOf(e); t != nil && types.Implements(t, s.reporter) {
		return true
	}

	// A METHOD of one, answered by its name on the interface and never by its
	// shape — Logf is Errorf's twin in shape, and a step that logs is not a
	// step that fails.
	if sel, ok := e.(*ast.SelectorExpr); ok {
		if m, ok := info.Selections[sel]; ok && m.Kind() == types.MethodVal {
			fn, ok := m.Obj().(*types.Func)
			return ok && failing[fn.Name()] && types.Implements(m.Recv(), s.reporter)
		}
	}

	// A name this package binds to one of those.
	if obj := s.objectOf(e); obj != nil {
		for _, bound := range s.bindings()[obj] {
			if s.isAWayToFail(bound, depth+1) {
				return true
			}
		}
	}

	// And the shape, for a value bound somewhere this cannot read.
	if t := info.TypeOf(e); t != nil {
		if sig, ok := t.Underlying().(*types.Signature); ok && types.Identical(sig, s.fails) {
			return true
		}
	}
	return false
}

// handedAWayToFail reports whether a call is GIVEN one in an argument.
// takesAReporter reads the callee's signature and cannot see this: a parameter
// of type func(string, ...any) has no Errorf method, so nothing about the
// declaration says the call can fail a test, and everything about the argument
// does.
func (s *source) handedAWayToFail(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		if s.isAWayToFail(arg, 0) {
			return true
		}
	}
	return false
}

type putDown struct {
	site  string
	where string
}

// waysToFailPutDown is every way to fail left in a struct field, a map or slice
// element, a channel, or a caller's hands: its type there is a callback's shape,
// so it is refused where it is put down. Keeping a reporter is allowed, since a
// call through one answers by its type.
func (s *source) waysToFailPutDown() []putDown {
	var out []putDown
	look := func(e ast.Expr, where string) {
		if e == nil {
			return
		}
		t := s.pkg.TypesInfo.TypeOf(e)
		if t == nil {
			return
		}
		sig, ok := t.Underlying().(*types.Signature)
		if !ok {
			return
		}
		if s.isAWayToFail(e, 0) || takesAReporter(sig, s.reporter) {
			out = append(out, putDown{s.site(e.Pos()), where})
		}
	}
	for _, file := range s.pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.CompositeLit:
				for _, el := range v.Elts {
					if kv, ok := el.(*ast.KeyValueExpr); ok {
						el = kv.Value
					}
					look(el, "a struct field, a map entry or a slice element")
				}
			case *ast.AssignStmt:
				for i, lhs := range v.Lhs {
					switch lhs.(type) {
					case *ast.SelectorExpr, *ast.IndexExpr:
						if i < len(v.Rhs) {
							look(v.Rhs[i], "a field or an element assigned to")
						}
					}
				}
			case *ast.SendStmt:
				look(v.Value, "a channel")
			case *ast.ReturnStmt:
				for _, e := range v.Results {
					look(e, "a caller's hands")
				}
			}
			return true
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].site < out[j].site })
	return out
}

func (s *source) objectOf(e ast.Expr) types.Object {
	info := s.pkg.TypesInfo
	switch v := ast.Unparen(e).(type) {
	case *ast.SelectorExpr:
		if sel, ok := info.Selections[v]; ok {
			return sel.Obj()
		}
		return info.Uses[v.Sel]
	case *ast.Ident:
		if obj := info.Uses[v]; obj != nil {
			return obj
		}
		return info.Defs[v]
	}
	return nil
}

// bindings is every value a name in this package is given: the right-hand side
// of the declaration or the assignment that binds it. It is read out of source
// for the same reason the population is — a name is not a value, and following
// one to what it was bound to is the difference between reading `var truthy =
// assert.True` as testify and reading it as nothing at all.
func (s *source) bindings() map[types.Object][]ast.Expr {
	s.boundOnce.Do(func() {
		out := map[types.Object][]ast.Expr{}
		bind := func(name *ast.Ident, value ast.Expr) {
			obj := s.pkg.TypesInfo.Defs[name]
			if obj == nil {
				obj = s.pkg.TypesInfo.Uses[name]
			}
			if obj != nil && value != nil {
				out[obj] = append(out[obj], value)
			}
		}
		for _, file := range s.pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.ValueSpec:
					for i, name := range v.Names {
						if i < len(v.Values) {
							bind(name, v.Values[i])
						}
					}
				case *ast.AssignStmt:
					if len(v.Lhs) != len(v.Rhs) {
						return true
					}
					for i, lhs := range v.Lhs {
						if id, ok := lhs.(*ast.Ident); ok {
							bind(id, v.Rhs[i])
						}
					}
				}
				return true
			})
		}
		s.bound = out
	})
	return s.bound
}

// markerFirings is every call that is a marker firing rather than a claim
// written in one: a failure through the reporter the marker was handed,
// reporting the question it was handed.
func (s *source) markerFirings() map[*ast.CallExpr]bool {
	s.firingOnce.Do(func() {
		out := map[*ast.CallExpr]bool{}
		for _, file := range s.pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				body, sig := s.bodyAndSignature(n)
				if body == nil || sig == nil {
					return true
				}
				at := s.stepParam(sig)
				if at < 0 {
					return true
				}
				question := sig.Params().At(at)
				for _, call := range callsDirectlyIn(body) {
					fn, recv := s.callee(call)
					if fn == nil || recv == nil || !failing[fn.Name()] || !types.Implements(recv, s.reporter) {
						continue
					}
					if s.mentions(call, question) {
						out[call] = true
					}
				}
				return true
			})
		}
		s.firing = out
	})
	return s.firing
}

func (s *source) mentions(call *ast.CallExpr, obj types.Object) bool {
	found := false
	ast.Inspect(call, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && s.pkg.TypesInfo.Uses[id] == obj {
			found = true
		}
		return true
	})
	return found
}

// bodyAndSignature is what a node holds if it holds a function: a declaration
// and a literal are the two places Go keeps a body, and this answers for both.
func (s *source) bodyAndSignature(n ast.Node) (*ast.BlockStmt, *types.Signature) {
	switch v := n.(type) {
	case *ast.FuncDecl:
		obj, _ := s.pkg.TypesInfo.Defs[v.Name].(*types.Func)
		if obj == nil {
			return nil, nil
		}
		sig, _ := obj.Type().(*types.Signature)
		return v.Body, sig
	case *ast.FuncLit:
		sig, _ := s.pkg.TypesInfo.TypeOf(v).(*types.Signature)
		return v.Body, sig
	}
	return nil, nil
}

// spell is how a call reads where it is written, which is what a failure
// message should quote back. It is a label and never an identity.
func (s *source) spell(call *ast.CallExpr) string {
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.SelectorExpr:
		if x, ok := ast.Unparen(fun.X).(*ast.Ident); ok {
			return x.Name + "." + fun.Sel.Name
		}
		return fun.Sel.Name
	case *ast.Ident:
		return fun.Name
	}
	return types.ExprString(call.Fun)
}

// stepParam is where a marker's question sits, or -1 for a function that asks
// for no step.
func (s *source) stepParam(sig *types.Signature) int {
	for i := 0; i < sig.Params().Len(); i++ {
		if types.Identical(sig.Params().At(i).Type(), s.step) {
			return i
		}
	}
	return -1
}

// takesAReporter reports whether a function is handed something it can fail
// through — testify's TestingT and everything shaped like it.
func takesAReporter(sig *types.Signature, reporter *types.Interface) bool {
	for i := 0; i < sig.Params().Len(); i++ {
		if types.Implements(sig.Params().At(i).Type(), reporter) {
			return true
		}
	}
	return false
}

// messageArg is which argument carries the message, read off the signature
// rather than out of a table of call names.
//
// The two shapes are told apart by the parameter BEFORE the variadic. Errorf
// and every marker read (format string, args ...any), so the message is that
// string. testify reads (t, expected, actual, msgAndArgs ...any), so the
// message is the head of the variadic itself. A call whose message argument is
// not a string literal ends up saying nothing, and an assertion that says
// nothing is a failure of its own — so a signature this reads wrongly is loud.
func messageArg(sig *types.Signature) int {
	n := sig.Params().Len()
	if !sig.Variadic() || n < 2 {
		return -1
	}
	if basic, ok := sig.Params().At(n - 2).Type().(*types.Basic); ok && basic.Kind() == types.String {
		return n - 2
	}
	return n - 1
}

// called is what a call calls, whatever that is: a function, a method, or a
// closure a function bound to a name. Objects and not names, so two closures
// spelled the same in two files are two things.
func (s *source) called(call *ast.CallExpr) types.Object {
	info := s.pkg.TypesInfo
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.SelectorExpr:
		if sel, ok := info.Selections[fun]; ok {
			return sel.Obj()
		}
		return info.Uses[fun.Sel]
	case *ast.Ident:
		return info.Uses[fun]
	}
	return nil
}

// asserts reports whether a body makes a claim of its own — a real assertion
// and not a step.
func (s *source) asserts(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if f, ok := s.howItFails(call); ok && f.kind == contract {
			found = true
		}
		return true
	})
	return found
}

// position spells a node the way a stack frame does.
func (s *source) position(p token.Pos) token.Position { return s.pkg.Fset.Position(p) }

func (s *source) site(p token.Pos) string {
	pos := s.position(p)
	return fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line)
}

func population(t *testing.T) []assertion {
	t.Helper()
	return populationOf(suiteSource(t))
}

func populationOf(src *source) []assertion {
	w := src.walk()
	callers := src.callers(w)

	var out []assertion
	seenStep := map[string]bool{}
	for _, c := range w.order {
		for _, call := range src.assertionsIn(c, w) {
			f, _ := src.howItFails(call)
			site := src.site(call.Pos())

			// A step needs no fault, so splitting one into cases buys nothing
			// and costs the ledger a way to tell two of them apart. One entry,
			// at the site where it is written.
			if f.kind == plumbing {
				if seenStep[site] {
					continue
				}
				seenStep[site] = true
				out = append(out, assertion{
					id: site, site: site, call: f.spelling, in: c.name, kind: plumbing,
					says: literalArg(call, f.at),
				})
				continue
			}

			format := literalArg(call, f.at)
			var args []ast.Expr
			if f.at+1 <= len(call.Args) {
				args = call.Args[f.at+1:]
			}
			for _, chain := range src.chainsFor(c, args, callers) {
				bound, id, where := src.bind(chain), site, ""
				for _, e := range chain {
					where = src.site(e.call.Pos())
					id += " via " + where
				}
				out = append(out, assertion{
					id: id, site: site, where: where, call: f.spelling, in: c.name, kind: contract,
					says: src.render(format, call, f.at, bound),
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

type walk struct {
	byObj map[types.Object]*callable
	byLit map[*ast.FuncLit]*callable
	order []*callable
}

// walk is every function, method, named closure and stray literal in the suite,
// in a stable order. Every declaration is a root; every literal bound to a name
// is a root, so a case passes through the name; every other literal is walked
// by its container, and one nothing contains is a root of its own.
func (s *source) walk() *walk {
	s.walkOnce.Do(func() { s.theWalk = s.everyBody() })
	return s.theWalk
}

func (s *source) everyBody() *walk {
	w := &walk{byObj: map[types.Object]*callable{}, byLit: map[*ast.FuncLit]*callable{}}
	add := func(obj types.Object, name string, body *ast.BlockStmt, sig *types.Signature, lit *ast.FuncLit) {
		if body == nil || sig == nil {
			return
		}
		if obj != nil && w.byObj[obj] != nil {
			return
		}
		if lit != nil && w.byLit[lit] != nil {
			return
		}
		var params []*types.Var
		for i := 0; i < sig.Params().Len(); i++ {
			params = append(params, sig.Params().At(i))
		}
		c := &callable{obj: obj, name: name, file: filepath.Base(s.position(body.Pos()).Filename),
			body: body, params: params, lit: lit}
		if obj != nil {
			w.byObj[obj] = c
		}
		if lit != nil {
			w.byLit[lit] = c
		}
		w.order = append(w.order, c)
	}
	bindLit := func(name *ast.Ident, value ast.Expr) {
		lit, ok := ast.Unparen(value).(*ast.FuncLit)
		if !ok {
			return
		}
		obj := s.pkg.TypesInfo.Defs[name]
		if obj == nil {
			obj = s.pkg.TypesInfo.Uses[name]
		}
		sig, _ := s.pkg.TypesInfo.TypeOf(lit).(*types.Signature)
		add(obj, name.Name, lit.Body, sig, lit)
	}
	for _, file := range s.pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.FuncDecl:
				obj, _ := s.pkg.TypesInfo.Defs[v.Name].(*types.Func)
				if obj == nil {
					return true
				}
				sig, _ := obj.Type().(*types.Signature)
				add(obj, v.Name.Name, v.Body, sig, nil)
			case *ast.ValueSpec:
				for i, name := range v.Names {
					if i < len(v.Values) {
						bindLit(name, v.Values[i])
					}
				}
			case *ast.AssignStmt:
				if len(v.Lhs) != len(v.Rhs) {
					return true
				}
				for i, lhs := range v.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						bindLit(id, v.Rhs[i])
					}
				}
			}
			return true
		})
	}
	// And every literal nothing above reaches, outermost first. This is the
	// clause that makes the walk complete rather than a list of the places a
	// body has been bound so far.
	for {
		stray := s.aBodyNothingWalks(w)
		if stray == nil {
			break
		}
		sig, _ := s.pkg.TypesInfo.TypeOf(stray).(*types.Signature)
		add(nil, "a function literal at "+s.site(stray.Pos()), stray.Body, sig, stray)
		if w.byLit[stray] == nil {
			break // a literal with no signature: nothing more to be done with it
		}
	}
	return w
}

func (s *source) aBodyNothingWalks(w *walk) *ast.FuncLit {
	reached := s.reaches(w)
	var found *ast.FuncLit
	for _, file := range s.pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			if found != nil {
				return false
			}
			lit, ok := n.(*ast.FuncLit)
			if !ok {
				return true
			}
			if reached[lit.Body] {
				return true
			}
			found = lit
			return false
		})
		if found != nil {
			return found
		}
	}
	return nil
}

// reaches is every body the roots enter: their own, and every literal written
// inside one that is not a root itself.
func (s *source) reaches(w *walk) map[*ast.BlockStmt]bool {
	out := map[*ast.BlockStmt]bool{}
	for _, c := range w.order {
		out[c.body] = true
		s.walkOwn(c, w, func(n ast.Node) bool {
			if lit, ok := n.(*ast.FuncLit); ok {
				out[lit.Body] = true
			}
			return true
		})
	}
	return out
}

// unwalkedBodies is every body in this package that the given walk does not
// enter, by the site it is written at. Over the walk above it is empty by
// construction; it is computed and reported anyway, because "by construction"
// is a claim about code somebody will edit, and an assertion in a body nothing
// walks is not exempt, not counted and not anywhere.
func (s *source) unwalkedBodies(w *walk) []string {
	reached := s.reaches(w)
	var out []string
	for _, file := range s.pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			body, sig := s.bodyAndSignature(n)
			if body == nil || sig == nil || reached[body] {
				return true
			}
			out = append(out, s.site(body.Pos()))
			return true
		})
	}
	sort.Strings(out)
	return out
}

// callers is who calls what, which is how a case knows where it came through.
func (s *source) callers(w *walk) map[*callable][]edge {
	out := map[*callable][]edge{}
	for _, c := range w.order {
		s.walkOwn(c, w, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if to := w.byObj[s.called(call)]; to != nil {
				out[to] = append(out[to], edge{from: c, to: to, call: call})
			}
			return true
		})
	}
	return out
}

// walkOwn walks the part of a callable's body that is ITS OWN: a literal that
// is a root in its own right is walked there and skipped here, and every other
// literal is part of the body it is written in.
func (s *source) walkOwn(c *callable, w *walk, f func(ast.Node) bool) {
	ast.Inspect(c.body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok && lit != c.lit && w.byLit[lit] != nil {
			return false
		}
		return f(n)
	})
}

func (s *source) assertionsIn(c *callable, w *walk) []*ast.CallExpr {
	var out []*ast.CallExpr
	s.walkOwn(c, w, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if _, ok := s.howItFails(call); ok {
			out = append(out, call)
		}
		return true
	})
	return out
}

// chainsFor is every chain of call sites reaching one assertion, climbing only
// while a word of its message is still a parameter someone else decides.
// Further would multiply cases (Run is called from two branches); shorter would
// leave cases saying the same thing.
func (s *source) chainsFor(c *callable, args []ast.Expr, callers map[*callable][]edge) [][]edge {
	type frame struct {
		at    *callable
		chain []edge
		alias map[types.Object]*types.Var // an argument, and the parameter that still decides it
	}
	start := frame{at: c, alias: map[types.Object]*types.Var{}}
	for _, arg := range args {
		id, ok := ast.Unparen(arg).(*ast.Ident)
		if !ok {
			continue
		}
		obj := s.pkg.TypesInfo.Uses[id]
		if v := paramAt(c, obj); v != nil {
			start.alias[obj] = v
		}
	}

	var out [][]edge
	queue := []frame{start}
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		if len(f.alias) == 0 || len(callers[f.at]) == 0 || len(f.chain) >= 8 {
			out = append(out, f.chain)
			continue
		}
		for _, e := range callers[f.at] {
			next := frame{at: e.from, chain: append(append([]edge{}, f.chain...), e), alias: map[types.Object]*types.Var{}}
			for obj, param := range f.alias {
				i := indexOf(f.at.params, param)
				if i < 0 || i >= len(e.call.Args) {
					continue
				}
				arg := ast.Unparen(e.call.Args[i])
				if _, ok := constantArg(s.pkg.TypesInfo, arg); ok {
					continue // decided here; nothing left to climb for
				}
				if id, ok := arg.(*ast.Ident); ok {
					if v := paramAt(e.from, s.pkg.TypesInfo.Uses[id]); v != nil {
						next.alias[obj] = v
					}
				}
			}
			queue = append(queue, next)
		}
	}
	return out
}

func paramAt(c *callable, obj types.Object) *types.Var {
	v, ok := obj.(*types.Var)
	if !ok {
		return nil
	}
	for _, p := range c.params {
		if p == v {
			return v
		}
	}
	return nil
}

func indexOf(params []*types.Var, want *types.Var) int {
	for i, p := range params {
		if p == want {
			return i
		}
	}
	return -1
}

// bind is what the constants along one chain of call sites make of the
// arguments an assertion's message names. It is how "a delivery signed an hour
// ago" gets from a table row into the words of the assertion that row is a
// contract of.
func (s *source) bind(chain []edge) map[types.Object]string {
	// Innermost first: each edge decides its callee's parameters, and a
	// parameter passed straight through is decided one level further out.
	bound := map[types.Object]string{}
	alias := map[types.Object]*types.Var{}
	for _, e := range chain {
		next := map[types.Object]string{}
		nextAlias := map[types.Object]*types.Var{}
		for i, arg := range e.call.Args {
			if i >= len(e.to.params) {
				break
			}
			param := e.to.params[i]
			if v, ok := constantArg(s.pkg.TypesInfo, ast.Unparen(arg)); ok {
				next[param] = v
				continue
			}
			if id, ok := ast.Unparen(arg).(*ast.Ident); ok {
				if v := paramAt(e.from, s.pkg.TypesInfo.Uses[id]); v != nil {
					nextAlias[param] = v
				}
			}
		}
		// What this level decided, written onto whatever was waiting for it.
		for obj, param := range alias {
			if v, ok := next[param]; ok {
				bound[obj] = v
				continue
			}
			if v, ok := nextAlias[param]; ok {
				nextAlias[obj] = v
			}
		}
		for param, v := range next {
			bound[param] = v
		}
		alias = nextAlias
	}
	return bound
}

// render is what an assertion says in ONE case: its format, with the arguments
// this chain makes constant written in where their verbs are. An argument the
// chain does not decide keeps its verb, and containsFormat reads what is left
// the same way it always has.
func (s *source) render(format string, call *ast.CallExpr, at int, bound map[types.Object]string) string {
	args := call.Args
	if at+1 <= len(args) {
		args = args[at+1:]
	} else {
		args = nil
	}
	i := -1
	return verb.ReplaceAllStringFunc(format, func(v string) string {
		i++
		if i >= len(args) {
			return v
		}
		value, ok := constantArg(s.pkg.TypesInfo, args[i])
		if !ok {
			if id, ok2 := ast.Unparen(args[i]).(*ast.Ident); ok2 {
				value, ok = bound[s.pkg.TypesInfo.Uses[id]]
			}
		}
		if !ok {
			return v
		}
		if strings.HasSuffix(v, "q") {
			return strconv.Quote(value)
		}
		return value
	})
}

// literalArg is the message a call carries, or "" for a call whose message is
// not written down where it is made.
func literalArg(call *ast.CallExpr, at int) string {
	if at < 0 || at >= len(call.Args) {
		return ""
	}
	lit, ok := call.Args[at].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return strings.Trim(lit.Value, "`\"")
	}
	return s
}

// constantArg is the value of an argument the compiler already knows — a
// string literal, and a typed constant like forge.CapList, which is how one
// call site of a shared helper is told from another.
func constantArg(info *types.Info, arg ast.Expr) (string, bool) {
	tv, ok := info.Types[arg]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

// trace is where testify writes the assertion's own call site. It is the first
// frame, because everything above it is the suite's own stack.
var trace = regexp.MustCompile(`Error Trace:\s+\S*?([^/\s]+\.go:\d+)`)

// attribute turns what the recorder captured into the cases that produced it,
// keeping each one's words. The trace names the site; where a site holds
// several cases, the message's words pick one. A failure it cannot place is
// reported.
func attribute(pop []assertion, msgs []string) (map[string][]string, []complaint) {
	atSite := map[string][]assertion{}
	for _, a := range pop {
		atSite[a.site] = append(atSite[a.site], a)
	}
	var said []complaint
	fired := make(map[string][]string)
	for _, msg := range msgs {
		candidates := pop
		if g := trace.FindStringSubmatch(msg); g != nil {
			candidates = atSite[g[1]]
			if len(candidates) == 0 {
				said = append(said, complaint{ruleFailureFromNowhere, g[1],
					"a failure came from here, which is not an assertion this file found in the suite's source:\n" + msg})
				continue
			}
			if len(candidates) == 1 {
				fired[candidates[0].id] = append(fired[candidates[0].id], msg)
				continue
			}
		}
		var hits []string
		for _, a := range candidates {
			if a.says != "" && containsFormat(msg, a.says) {
				hits = append(hits, a.id)
			}
		}
		if len(hits) != 1 {
			said = append(said, complaint{ruleUnplaceable, fmt.Sprintf("%v", hits),
				fmt.Sprintf("a failure names %d cases and must name one; it is unattributable, so nothing here can say which one is load-bearing:\n%s", len(hits), msg)})
			continue
		}
		fired[hits[0]] = append(fired[hits[0]], msg)
	}
	return fired, said
}

// containsFormat reports whether a rendered message came from this format
// string: every literal run between the verbs, in order. "%s answered %q" is
// matched by "acme answered \"pay\"" and not by a message that only shares one
// half of it.
func containsFormat(msg, format string) bool {
	for _, chunk := range literalRuns(format) {
		i := strings.Index(msg, chunk)
		if i < 0 {
			return false
		}
		msg = msg[i+len(chunk):]
	}
	return true
}

// literalRuns is a format string's literal halves, with the verbs taken out.
// Runs shorter than four characters are dropped: they carry no identity, and a
// format that is nothing but verbs and punctuation matches everything.
func literalRuns(format string) []string {
	var runs []string
	for _, run := range verb.Split(format, -1) {
		if len(strings.TrimSpace(run)) >= 4 {
			runs = append(runs, run)
		}
	}
	if len(runs) == 0 {
		runs = []string{format}
	}
	return runs
}

var verb = regexp.MustCompile(`%[-+# 0-9.*]*[a-zA-Z]`)
