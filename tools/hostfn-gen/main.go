// Command hostfn-gen emits reflection-free host-function registration for the
// W_-prefixed methods across the plugin factory packages. For each
// `func (r *T) W_name(ctx, module, ...)` it generates
// `func (r *T) HostFunctions() []*vm.HostModuleFunctionDefinition` returning one
// entry per method, built with the typed vm.HostFnN/HostProcN helpers (Go infers
// the wasm signature from the method value — no reflection). Arity > 8 (beyond
// the helper family) is emitted as an inline Stack def.
//
// It scans whole roots so it can resolve embedding: a factory that embeds other
// provider types (e.g. resource.Factory embeds *database.Database, ...) has
// their host functions aggregated into its HostFunctions(), matching the old
// reflection behavior where promoted W_ methods were discovered.
//
// Usage: hostfn-gen -roots pkg/vm-low-orbit,pkg/vm-ops-orbit -out zz_generated_hostfn.go
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type method struct {
	recv      string
	name      string // export name, W_ stripped
	wname     string // original W_ method name
	params    []string
	hasResult bool
	resultVT  string
}

type embed struct {
	field      string // selector to reach it, e.g. "Database"
	importPath string // resolved package import path of the embedded type
	typ        string // embedded type name
}

type pkgData struct {
	dir        string
	name       string
	importPath string
	buildTag   string
	methods    []method
	// embeds[typeName] = anonymous fields of that struct
	embeds map[string][]embed
}

func main() {
	roots := flag.String("roots", "pkg/vm-low-orbit,pkg/vm-ops-orbit", "comma-separated roots to scan")
	out := flag.String("out", "zz_generated_hostfn.go", "generated file basename (per package)")
	flag.Parse()

	module := modulePath()

	var pkgs []*pkgData
	seen := map[string]bool{}
	for _, root := range strings.Split(*roots, ",") {
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() || seen[path] {
				return nil
			}
			seen[path] = true
			if p := scanDir(path, *out, module); p != nil {
				pkgs = append(pkgs, p)
			}
			return nil
		})
	}

	// Global provider set: types that own >=1 W_ method, keyed by
	// importPath.TypeName so embeds resolve unambiguously (many packages define
	// a type named "Factory"/"Storage"/"Database"; only the ones from the right
	// package are providers).
	providers := map[string]bool{}
	for _, p := range pkgs {
		for _, m := range p.methods {
			providers[p.importPath+"."+m.recv] = true
		}
	}

	for _, p := range pkgs {
		src, ok := render(p, providers)
		if !ok {
			continue
		}
		formatted, err := format.Source([]byte(src))
		if err != nil {
			fatal(fmt.Errorf("formatting %s: %w\n%s", p.dir, err, src))
		}
		if err := os.WriteFile(filepath.Join(p.dir, *out), formatted, 0o644); err != nil {
			fatal(err)
		}
		fmt.Printf("hostfn-gen: %s -> %s\n", p.name, filepath.Join(p.dir, *out))
	}
}

func scanDir(dir, out, module string) *pkgData {
	fset := token.NewFileSet()
	parsed, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		n := fi.Name()
		return !strings.HasSuffix(n, "_test.go") && n != out
	}, 0)
	if err != nil {
		fatal(err)
	}

	p := &pkgData{dir: dir, importPath: module + "/" + filepath.ToSlash(dir), embeds: map[string][]embed{}}
	var hasContent bool
	for name, pkg := range parsed {
		p.name = name
		for fname, file := range pkg.Files {
			imports := fileImports(file)
			var fileHasW bool
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil || !strings.HasPrefix(d.Name.Name, "W_") {
						continue
					}
					p.methods = append(p.methods, parseMethod(d))
					fileHasW = true
					hasContent = true
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						ts, ok := spec.(*ast.TypeSpec)
						if !ok {
							continue
						}
						st, ok := ts.Type.(*ast.StructType)
						if !ok {
							continue
						}
						if e := structEmbeds(st, imports, p.importPath); len(e) > 0 {
							p.embeds[ts.Name.Name] = e
						}
					}
				}
			}
			if fileHasW {
				if tag := fileBuildTag(fname); tag != "" && p.buildTag == "" {
					p.buildTag = tag
				}
			}
		}
	}
	if !hasContent {
		return nil
	}
	return p
}

// fileImports maps each import's local name to its import path. Unaliased
// imports are keyed by the path's last segment (the usual package name).
func fileImports(file *ast.File) map[string]string {
	m := map[string]string{}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		m[name] = path
	}
	return m
}

// structEmbeds returns the anonymous (embedded) fields of a struct with the
// import path of each embedded type resolved. selfPath is this package's import
// path (used for same-package embeds).
func structEmbeds(st *ast.StructType, imports map[string]string, selfPath string) []embed {
	var out []embed
	for _, f := range st.Fields.List {
		if len(f.Names) != 0 {
			continue // named field, not embedded
		}
		t := f.Type
		if star, ok := t.(*ast.StarExpr); ok {
			t = star.X
		}
		switch e := t.(type) {
		case *ast.SelectorExpr:
			if x, ok := e.X.(*ast.Ident); ok {
				out = append(out, embed{field: e.Sel.Name, importPath: imports[x.Name], typ: e.Sel.Name})
			}
		case *ast.Ident:
			out = append(out, embed{field: e.Name, importPath: selfPath, typ: e.Name})
		}
	}
	return out
}

// modulePath reads the module path from go.mod in the working directory.
func modulePath() string {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		fatal(fmt.Errorf("reading go.mod (run from repo root): %w", err))
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	fatal(fmt.Errorf("no module line in go.mod"))
	return ""
}

func parseMethod(fn *ast.FuncDecl) method {
	m := method{
		wname: fn.Name.Name,
		name:  strings.TrimPrefix(fn.Name.Name, "W_"),
		recv:  recvType(fn.Recv.List[0].Type),
	}
	var all []ast.Expr
	for _, f := range fn.Type.Params.List {
		n := len(f.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			all = append(all, f.Type)
		}
	}
	for _, t := range all[min(2, len(all)):] {
		m.params = append(m.params, exprString(t))
	}
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
		m.hasResult = true
		m.resultVT = valueType(exprString(fn.Type.Results.List[0].Type))
	}
	return m
}

func recvType(e ast.Expr) string {
	if s, ok := e.(*ast.StarExpr); ok {
		e = s.X
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return exprString(e)
}

// render emits the file for one package. Returns false if nothing to emit.
func render(p *pkgData, providers map[string]bool) (string, bool) {
	// receiver type -> own methods
	byRecv := map[string][]method{}
	var recvs []string
	for _, m := range p.methods {
		if _, ok := byRecv[m.recv]; !ok {
			recvs = append(recvs, m.recv)
		}
		byRecv[m.recv] = append(byRecv[m.recv], m)
	}
	// A type also needs HostFunctions() if it embeds a provider (even with no
	// own W_ methods).
	for typ, embeds := range p.embeds {
		if _, ok := byRecv[typ]; ok {
			continue
		}
		for _, e := range embeds {
			if providers[e.importPath+"."+e.typ] {
				recvs = append(recvs, typ)
				byRecv[typ] = nil
				break
			}
		}
	}
	if len(recvs) == 0 {
		return "", false
	}
	sort.Strings(recvs)
	for _, r := range recvs {
		sort.Slice(byRecv[r], func(i, j int) bool { return byRecv[r][i].name < byRecv[r][j].name })
	}

	var b strings.Builder
	b.WriteString("// Code generated by hostfn-gen; DO NOT EDIT.\n\n")
	if p.buildTag != "" {
		fmt.Fprintf(&b, "//go:build %s\n\n", p.buildTag)
	}
	fmt.Fprintf(&b, "package %s\n\n", p.name)
	b.WriteString("import (\n\t\"context\"\n\n\t\"github.com/taubyte/tau/core/vm\"\n)\n\n")
	b.WriteString("var _ = context.Background // keep context imported even if unused\n\n")

	for _, recv := range recvs {
		var embeds []embed
		for _, e := range p.embeds[recv] {
			if providers[e.importPath+"."+e.typ] {
				embeds = append(embeds, e)
			}
		}
		sort.Slice(embeds, func(i, j int) bool { return embeds[i].field < embeds[j].field })

		fmt.Fprintf(&b, "func (x *%s) HostFunctions() []*vm.HostModuleFunctionDefinition {\n", recv)
		if len(embeds) == 0 {
			b.WriteString("\treturn []*vm.HostModuleFunctionDefinition{\n")
			for _, m := range byRecv[recv] {
				b.WriteString(entry(m))
			}
			b.WriteString("\t}\n}\n\n")
			continue
		}
		b.WriteString("\tdefs := []*vm.HostModuleFunctionDefinition{\n")
		for _, m := range byRecv[recv] {
			b.WriteString(entry(m))
		}
		b.WriteString("\t}\n")
		for _, e := range embeds {
			fmt.Fprintf(&b, "\tdefs = append(defs, x.%s.HostFunctions()...)\n", e.field)
		}
		b.WriteString("\treturn defs\n}\n\n")
	}
	return b.String(), true
}

func entry(m method) string {
	n := len(m.params)
	if n <= 8 {
		helper := fmt.Sprintf("HostProc%d", n)
		if m.hasResult {
			helper = fmt.Sprintf("HostFn%d", n)
		}
		return fmt.Sprintf("\t\tvm.%s(%q, x.%s),\n", helper, m.name, m.wname)
	}
	var pt, args strings.Builder
	for i, p := range m.params {
		if i > 0 {
			pt.WriteString(", ")
			args.WriteString(", ")
		}
		pt.WriteString(valueType(p))
		fmt.Fprintf(&args, "%s(s[%d])", p, i)
	}
	var b strings.Builder
	b.WriteString("\t\t{\n")
	fmt.Fprintf(&b, "\t\t\tName:       %q,\n", m.name)
	fmt.Fprintf(&b, "\t\t\tParamTypes: []vm.ValueType{%s},\n", pt.String())
	if m.hasResult {
		fmt.Fprintf(&b, "\t\t\tResultTypes: []vm.ValueType{%s},\n", m.resultVT)
		fmt.Fprintf(&b, "\t\t\tStack: func(ctx context.Context, m vm.Module, s []uint64) { s[0] = uint64(x.%s(ctx, m, %s)) },\n", m.wname, args.String())
	} else {
		fmt.Fprintf(&b, "\t\t\tStack: func(ctx context.Context, m vm.Module, s []uint64) { x.%s(ctx, m, %s) },\n", m.wname, args.String())
	}
	b.WriteString("\t\t},\n")
	return b.String()
}

// valueType maps a Go type expr string to its vm.ValueType. Named types (e.g.
// errno.Error) are the errno-style uint32 result, so they map to i32.
func valueType(t string) string {
	switch t {
	case "uint64", "int64":
		return "vm.ValueTypeI64"
	default:
		return "vm.ValueTypeI32"
	}
}

func exprString(e ast.Expr) string {
	var b bytes.Buffer
	if err := format.Node(&b, token.NewFileSet(), e); err != nil {
		return "?"
	}
	return b.String()
}

func fileBuildTag(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//go:build ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "//go:build"))
		}
		if line != "" && !strings.HasPrefix(line, "//") {
			break
		}
	}
	return ""
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "hostfn-gen:", err)
	os.Exit(1)
}
