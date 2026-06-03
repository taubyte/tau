package main

import (
	"embed"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

// witFS holds the wasi:http WIT (proxy world + deps) the StarlingMonkey
// componentizer builds against. Vendored so the adapter is self-contained.
//
//go:embed all:wit
var witFS embed.FS

// writeWIT materialises the embedded WIT tree under dir/wit and returns its path.
func writeWIT(dir string) (string, error) {
	root := filepath.Join(dir, "wit")
	err := fs.WalkDir(witFS, "wit", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, p)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := witFS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
	if err != nil {
		return "", err
	}
	return root, nil
}

// componentizeJS compiles a bundled JS module into a wasi:http/proxy component
// with StarlingMonkey via jco. stdio is disabled so the component only imports
// what `wasmtime serve` provides plus wasi:cli (enabled at serve time). The
// invocation is overridable with TAUBYTE_JCO_ARGS (space-separated; %IN, %OUT
// and %WIT are substituted).
func componentizeJS(in, out, witDir string) error {
	args := []string{"componentize", in, "--wit", witDir, "--world-name", "wasi:http/proxy", "--disable", "stdio", "-o", out}
	if override := os.Getenv("TAUBYTE_JCO_ARGS"); override != "" {
		args = splitArgs(override, in, out, witDir)
	}
	if jco, err := exec.LookPath("jco"); err == nil {
		return runCmd(jco, args...)
	}
	// Fall back to npx so a global install isn't required.
	return runCmd("npx", append([]string{"--yes", "@bytecodealliance/jco"}, args...)...)
}

func splitArgs(s, in, out, wit string) []string {
	var fields []string
	for _, f := range splitFields(s) {
		switch f {
		case "%IN":
			f = in
		case "%OUT":
			f = out
		case "%WIT":
			f = wit
		}
		fields = append(fields, f)
	}
	return fields
}

// splitFields is strings.Fields without importing strings here twice; kept local
// and tiny to avoid surprises with the override string.
func splitFields(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
