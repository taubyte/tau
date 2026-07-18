package main

import (
	"fmt"
	"os"
	"path/filepath"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v1/schema"
	"github.com/taubyte/tau/tools/tcc-gen/internal/gen"
)

// defaultTSOut is where the generated schema.ts lands when --out is not given:
// under the @taubyte/tcc package src so tsc picks it up. Redirectable via --out.
const defaultTSOut = "pkg/tcc/clients/js/src/gen"

// writeTS renders the DSL resource interfaces to schema.ts.
func writeTS(root, out string) error {
	if out == "" {
		out = filepath.Join(root, defaultTSOut)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	data, err := gen.GenerateTS(schema.GenerationRoot())
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
