package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// defaultWasmOut is where the browser artifacts land when --out is not given:
// the @taubyte/tcc npm package's assets dir. It is only a default — --out
// redirects it (e.g. to a temp dir for tests, or a Go package for //go:embed).
const defaultWasmOut = "pkg/tcc/clients/js/assets"

// buildWasm compiles pkg/tcc/wasm for the browser (GOOS=js GOARCH=wasm) and
// copies the matching wasm_exec.js loader next to it.
func buildWasm(root, out string) error {
	if out == "" {
		out = filepath.Join(root, defaultWasmOut)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}

	wasmPath := filepath.Join(out, "tcc.wasm")
	cmd := exec.Command("go", "build", "-o", wasmPath, "./pkg/tcc/wasm")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building tcc.wasm failed: %w", err)
	}

	execSrc, err := wasmExecPath()
	if err != nil {
		return err
	}
	execDst := filepath.Join(out, "wasm_exec.js")
	if err := copyFile(execSrc, execDst); err != nil {
		return fmt.Errorf("copying wasm_exec.js failed: %w", err)
	}

	fmt.Printf("built %s and %s\n", wasmPath, execDst)
	return nil
}

// wasmExecPath locates wasm_exec.js in GOROOT. Go 1.24+ moved it from
// misc/wasm to lib/wasm; check both.
func wasmExecPath() (string, error) {
	goroot, err := goEnv("GOROOT")
	if err != nil {
		return "", err
	}
	for _, rel := range []string{"lib/wasm/wasm_exec.js", "misc/wasm/wasm_exec.js"} {
		p := filepath.Join(goroot, rel)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("wasm_exec.js not found under GOROOT %s", goroot)
}

func goEnv(name string) (string, error) {
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		return "", fmt.Errorf("go env %s failed: %w", name, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
