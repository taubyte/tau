package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/taubyte/tau/tools/tcc-gen/internal/gen"
)

// writeTS renders the selected DSL's resource interfaces to schema.ts. The default
// --out is the vocabulary's own (dslTarget.tsOut); --out redirects it.
func writeTS(root, out string, dsl dslTarget) error {
	if out == "" {
		out = filepath.Join(root, dsl.tsOut)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	data, err := gen.GenerateTS(dsl.compileRoot)
	if err != nil {
		return err
	}
	dst := filepath.Join(out, "schema.ts")
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", dst)
	return nil
}
