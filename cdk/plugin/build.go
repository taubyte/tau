package main

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/pterm/pterm"
	containers "github.com/taubyte/go-simple-container"
	build "github.com/taubyte/tau/pkg/builder"
	"github.com/taubyte/tau/utils"
)

func prepSource() (tempDir string, err error) {
	tempDir, err = os.MkdirTemp("/tmp", "*")
	if err != nil {
		err = fmt.Errorf("creating temp dir failed with: %w", err)
		return
	}

	pterm.Success.Printfln("Building code in: %s", tempDir)

	utils.CopyDir("wasm", path.Join(tempDir))

	utils.DuplicateModFile(
		"../../go.mod",
		path.Join(tempDir, "go.mod"),
		utils.ModRename("cdk"),
		utils.Replace("github.com/spf13/afero", "/afero"),
		utils.Replace("github.com/taubyte/tau", "/tau"),
	)

	return
}

// Wasm builds the a wasm file from the given directory
func Wasm(ctx context.Context, buildDir string) (wasmFile string, err error) {
	builder, err := build.New(ctx, buildDir)
	if err != nil {
		err = fmt.Errorf("new builder failed with: %w", err)
		return
	}

	out, err := builder.Build(
		containers.Volume(utils.SafeAbs("../.."), "/tau"),
	)

	out.Logs().CopyTo(os.Stdout)

	if err != nil {
		err = fmt.Errorf("builder.Build() failed with: %w", err)
		return
	}

	wasmFile = path.Join(out.OutDir(), "artifact.wasm")
	return
}

func main() {
	srcDir, err := prepSource()
	if err != nil {
		panic(err)
	}

	defer os.RemoveAll(srcDir)

	wasmfile, err := Wasm(context.Background(), srcDir)
	if err != nil {
		panic(err)
	}

	err = utils.CopyFile(wasmfile, "../js/core.wasm")
	if err != nil {
		panic(err)
	}
}
