package upx

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed wasm/upx
var upxWasm []byte

type UPX struct {
	runtime wazero.Runtime
}

func New(ctx context.Context) (*UPX, error) {
	runtime := wazero.NewRuntime(ctx)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		runtime.Close(ctx)
		return nil, fmt.Errorf("failed to init WASI: %w", err)
	}
	return &UPX{runtime: runtime}, nil
}

// CompressFile compresses the given executable using the UPX WASM binary.
// It creates a compressed copy and preserves the original.
func (u *UPX) CompressFile(ctx context.Context, inputFile, outputFile string) error {
	if _, err := os.Stat(inputFile); err != nil {
		return fmt.Errorf("input file not found: %w", err)
	}

	absInput, err := filepath.Abs(inputFile)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for input: %w", err)
	}

	absOutput, err := filepath.Abs(outputFile)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for output: %w", err)
	}

	workDir := filepath.Dir(absInput)

	relInput, err := filepath.Rel(workDir, absInput)
	if err != nil {
		return fmt.Errorf("failed to get relative path for input: %w", err)
	}

	relOutput, err := filepath.Rel(workDir, absOutput)
	if err != nil {
		return fmt.Errorf("failed to get relative path for output: %w", err)
	}

	fsConfig := wazero.NewFSConfig().
		WithDirMount(workDir, ".")

	var stderr bytes.Buffer
	cfg := wazero.NewModuleConfig().
		WithArgs("upx", "-qq", "-o", relOutput, relInput).
		WithStdout(os.Stdout).
		WithStderr(&stderr).
		WithFSConfig(fsConfig)

	mod, err := u.runtime.InstantiateWithConfig(ctx, upxWasm, cfg)
	if err != nil {
		return fmt.Errorf("UPX failed: %v, stderr: %s", err, stderr.String())
	}
	defer mod.Close(ctx)

	if stderr.Len() > 0 {
		return fmt.Errorf("UPX reported errors: %s", stderr.String())
	}

	return nil
}

// Close releases all resources used by the wazero runtime.
func (u *UPX) Close(ctx context.Context) error {
	return u.runtime.Close(ctx)
}
