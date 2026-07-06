// Command tcc-gen generates the mechanical pkg/schema/<resource> accessor files
// from the tcc schema DSL (pkg/tcc/taubyte/v1/schema/definition.go), which is the
// single source of truth for resource fields.
//
//	tcc-gen [--out DIR]   write generated files under DIR (default: a temp dir)
//	tcc-gen --check       diff generated accessors against the current pkg/schema
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v1/schema"
	"github.com/taubyte/tau/tools/tcc-gen/internal/gen"
	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name:  "tcc-gen",
		Usage: "generate pkg/schema accessors from the tcc schema DSL",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "out", Usage: "output directory (default: a temp dir)"},
			&cli.StringFlag{Name: "root", Usage: "repo root (default: autodetected from cwd)"},
			&cli.BoolFlag{Name: "check", Usage: "diff generated accessors against pkg/schema and report drift"},
		},
		Action: run,
	}
	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
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

	generated, err := gen.Generate(schema.TaubyteRessources)
	if err != nil {
		return err
	}

	if c.Bool("check") {
		diffs, err := gen.Check(root, generated)
		if err != nil {
			return err
		}
		gen.PrintReport(os.Stdout, generated, diffs)
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
