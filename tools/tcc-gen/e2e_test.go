package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestGeneratedClientE2E generates the wasm module and TypeScript schema fresh
// into a tmp package and runs the client's tests against that generated code
// (pkg/tcc/clients/js/e2e.sh) — an end-to-end check of the whole
// DSL -> tcc-gen -> generated TS -> wasm pipeline.
//
// Opt-in: it needs node and the client's dev deps, so the normal `go test`
// sweep skips it. Enable with TCC_E2E=1.
func TestGeneratedClientE2E(t *testing.T) {
	if os.Getenv("TCC_E2E") == "" {
		t.Skip("set TCC_E2E=1 to run the generated-client e2e (needs node + client deps)")
	}
	script, err := filepath.Abs("../../pkg/tcc/clients/js/e2e.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("generated-client e2e failed: %v", err)
	}
}
