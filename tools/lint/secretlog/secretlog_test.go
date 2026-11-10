// Package secretlog holds a source scan that keeps secrets out of log calls.
package secretlog

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	scanDirs   = []string{"services", "pkg", "clients", "dream", "cli", "p2p"}
	secretName = regexp.MustCompile(`(?i)(secret|privkey|privatekey|token|password|passwd|apikey|deploykey|payload|certificate|\bkey\b)`)
	loggerName = regexp.MustCompile(`^(?i:logger|log)$`)
)

type hit struct{ path, line, text string }

// TestNoSecretsInLogs fails when a log call is handed a value whose name says
// it is a secret, or the request variables (ctx.Variables()). Reviewed false
// positives live in allowlist.txt.
func TestNoSecretsInLogs(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	allow := readAllowlist(t)
	used := make([]bool, len(allow))

	var leaks []string
	for _, h := range scan(t, root) {
		ok := false
		for i, a := range allow {
			if a.path == h.path && strings.Contains(h.text, a.substr) {
				used[i], ok = true, true
			}
		}
		if !ok {
			leaks = append(leaks, fmt.Sprintf("%s:%s: %s", h.path, h.line, strings.TrimSpace(h.text)))
		}
	}
	for _, l := range leaks {
		t.Errorf("possible secret in a log call: %s", l)
	}
	if len(leaks) > 0 {
		t.Log("stop logging the value, or add a reviewed `path:line-substring # reason` entry to allowlist.txt")
	}
	for i, a := range allow {
		if !used[i] {
			t.Errorf("stale allowlist entry (matches nothing): %s:%s", a.path, a.substr)
		}
	}
}

type allowEntry struct{ path, substr string }

func readAllowlist(t *testing.T) []allowEntry {
	f, err := os.Open("allowlist.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []allowEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.LastIndex(line, " # "); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		path, substr, found := strings.Cut(line, ":")
		if !found || substr == "" {
			t.Fatalf("bad allowlist line %q: want path:line-substring # reason", sc.Text())
		}
		out = append(out, allowEntry{path, substr})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func scan(t *testing.T, root string) []hit {
	var hits []hit
	fset := token.NewFileSet()
	for _, dir := range scanDirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if n := d.Name(); n == "testdata" || n == "vendor" || n == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			file, err := parser.ParseFile(fset, p, src, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			lines := strings.Split(string(src), "\n")
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			seen := map[int]bool{}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isLogCall(call.Fun) {
					return true
				}
				for _, arg := range call.Args {
					ast.Inspect(arg, func(n ast.Node) bool {
						if pos, bad := secretArg(n); bad {
							if l := fset.Position(pos).Line; !seen[l] {
								seen[l] = true
								hits = append(hits, hit{rel, fmt.Sprint(l), lines[l-1]})
							}
						}
						return true
					})
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].path+hits[i].line < hits[j].path+hits[j].line })
	return hits
}

// isLogCall matches logger.X / log.X (also s.logger.X), fmt.Print* and pterm.*.
func isLogCall(fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch x := sel.X.(type) {
	case *ast.Ident:
		if x.Name == "fmt" {
			return strings.HasPrefix(sel.Sel.Name, "Print")
		}
		return loggerName.MatchString(x.Name) || x.Name == "pterm"
	case *ast.SelectorExpr:
		if loggerName.MatchString(x.Sel.Name) {
			return true
		}
		for e := ast.Expr(x); ; { // pterm.Info.Printfln, pterm.Error.WithShowLineNumber(...).Println
			switch v := e.(type) {
			case *ast.SelectorExpr:
				e = v.X
				continue
			case *ast.CallExpr:
				e = v.Fun
				continue
			case *ast.Ident:
				return v.Name == "pterm"
			}
			return false
		}
	}
	return false
}

func secretArg(n ast.Node) (token.Pos, bool) {
	switch v := n.(type) {
	case *ast.Ident:
		return v.Pos(), secretName.MatchString(v.Name)
	case *ast.CallExpr:
		if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Variables" && len(v.Args) == 0 {
			return v.Pos(), true
		}
	}
	return 0, false
}
