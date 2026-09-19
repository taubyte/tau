// Command tcc-gen generates the mechanical legacy/schema/<resource> accessor files
// from the tcc schema DSL (pkg/tcc/taubyte/v1/schema/definition.go), which is the
// single source of truth for resource fields.
//
//	tcc-gen [--out DIR]   write generated files under DIR (default: a temp dir)
//	tcc-gen --check       diff generated accessors against the current legacy/schema
//	tcc-gen --wasm [--out DIR]  build the browser wasm module + wasm_exec.js
//	                      (default out: pkg/tcc/clients/js/assets)
//	tcc-gen --ts [--out DIR]    generate the TypeScript schema interfaces
//	                      (default out: pkg/tcc/clients/js/src/gen)
//
// --dsl v1|v2 selects the vocabulary every mode reads. It is the ONLY thing that
// differs between the two: accessors, TypeScript and the wasm module are all
// generated from whichever schema root is named, so a second DSL costs a flag
// rather than a second generator.
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
	v2 "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/tools/tcc-gen/internal/gen"
	"github.com/urfave/cli/v2"
)

func main() {
	if err := newApp().Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

// newApp is the command, built apart from main so a test can run it with
// arguments instead of shelling out to a binary it would have to build first.
func newApp() *cli.App {
	return &cli.App{
		Name:  "tcc-gen",
		Usage: "generate the JSON Schema, the explorer TypeScript and pkg/specs from the tcc schema DSL",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "out", Usage: "output directory (default: a temp dir)"},
			&cli.StringFlag{Name: "root", Usage: "repo root (default: autodetected from cwd)"},
			&cli.BoolFlag{Name: "check", Usage: "report which generated files on disk have drifted from the vocabulary"},
			&cli.BoolFlag{Name: "wasm", Usage: "build the browser wasm module (GOOS=js) + wasm_exec.js into --out"},
			&cli.BoolFlag{Name: "ts", Usage: "generate the TypeScript schema (interfaces) into --out"},
			&cli.StringFlag{Name: "dsl", Value: "v2", Usage: "which vocabulary to generate from"},
		},
		Action: run,
	}
}

func run(c *cli.Context) error {
	root := c.String("root")
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		if root, err = findRepoRoot(cwd); err != nil {
			return err
		}
	}

	dsl, err := selectDSL(c.String("dsl"))
	if err != nil {
		return err
	}

	if c.Bool("wasm") {
		return buildWasm(root, c.String("out"), dsl)
	}

	if c.Bool("ts") {
		return writeTS(root, c.String("out"), dsl)
	}

	generated, err := gen.Generate(dsl.generationRoot)
	if err != nil {
		return err
	}

	if c.Bool("check") {
		drifted, err := gen.Check(root, generated)
		if err != nil {
			return err
		}
		gen.PrintReport(os.Stdout, generated, drifted)
		return nil
	}

	out := c.String("out")
	if out == "" {
		if out, err = os.MkdirTemp("", "tcc-gen-"); err != nil {
			return err
		}
	}
	if err := gen.WriteTo(out, generated); err != nil {
		return err
	}
	fmt.Printf("wrote %d files to %s\n", len(generated), out)
	return nil
}

// dslTarget is everything the generator needs to know about a vocabulary: its two
// schema roots and where its browser artifacts belong. Adding a vocabulary is one
// entry here, not a fork of the generator.
type dslTarget struct {
	name           string
	generationRoot []*engine.Node
	compileRoot    *engine.Node
	wasmPkg        string // the main package built for GOOS=js
	wasmOut        string // default --out for --wasm
	tsOut          string // default --out for --ts
}

func selectDSL(name string) (dslTarget, error) {
	switch name {
	case "", "v2":
		return dslTarget{
			name:           "v2",
			generationRoot: v2.GenerationRoot(),
			compileRoot:    v2.CompileRoot(),
			wasmPkg:        "./pkg/tcc/taubyte/v2/wasm",
			wasmOut:        "pkg/tcc/taubyte/v2/explorer/public",
			tsOut:          "pkg/tcc/taubyte/v2/explorer/src/gen",
		}, nil
	}
	return dslTarget{}, fmt.Errorf("unknown --dsl %q: v2 is the only vocabulary", name)
}

// findRepoRoot walks up from dir to the tau module root.
func findRepoRoot(dir string) (string, error) {
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil &&
			strings.Contains(string(b), "module github.com/taubyte/tau") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("could not locate tau module root (go.mod) above cwd")
		}
		dir = parent
	}
}
