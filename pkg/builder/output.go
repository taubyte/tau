package builder

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/taubyte/tau/core/builders"
	spec "github.com/taubyte/tau/pkg/specs/builders"
	"github.com/taubyte/tau/pkg/specs/builders/wasm"
	"github.com/taubyte/tau/utils/bundle"
)

var DeprecatedWasmBuild bool

/*
	TODO: build Flag
	const DeprecatedWasmBuild

*/

// new sets the working directory and log file of the desired output
func new(wd spec.Dir) *output {
	return &output{
		wd: wd,
	}
}

// outputDirHasNoFiles returns true when dir has no regular files (only dirs or empty).
func outputDirHasNoFiles(dir string) (bool, error) {
	var hasFile bool
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			hasFile = true
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return !hasFile, nil
}

// zipOutput archives one file the build produced.
//
// The output directory is written by the build, so a path under it is resolved
// and required to stay under it before anything is read: a component along the
// way may name somewhere else entirely, and this runs outside the container.
func (o *output) zipOutput(source, target string) (*os.File, error) {
	root, err := filepath.EvalSymlinks(o.outDir)
	if err != nil {
		return nil, fmt.Errorf("resolving output directory failed with: %w", err)
	}

	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return nil, fmt.Errorf("resolving `%s` failed with: %w", source, err)
	}

	if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return nil, fmt.Errorf("refusing to archive `%s`: it resolves outside the output directory", source)
	}

	return bundle.Zip(bundle.ZipFile, resolved, target, wasm.WasmFile)
}

// Compress takes a CompressionMethod, and returns the compressed output of the files built by Build
func (o *output) Compress(method builders.CompressionMethod) (io.ReadSeekCloser, error) {
	var (
		zippedFile *os.File
		err        error
	)

	switch method {
	case builders.WASM:
		if DeprecatedWasmBuild {
			return o.handleDeprecated()
		}

		// Try for both artifact/main.wasm
		zippedFile, err = o.zipOutput(wasm.WasmOutput(o.outDir), o.wd.Wasm().Zip())
		if err != nil {
			zippedFile, err = o.zipOutput(wasm.WasmDeprecatedOutput(o.outDir), o.wd.Wasm().Zip())
		}
	case builders.Website:
		noFiles, err := outputDirHasNoFiles(o.outDir)
		if err != nil {
			return nil, fmt.Errorf("checking website output directory failed with: %w", err)
		}
		if noFiles {
			return nil, fmt.Errorf("website build produced no output: output directory is empty")
		}
		zippedFile, err = bundle.Zip(bundle.ZipDir, o.outDir, o.wd.Website().BuildZip())
	default:
		return nil, fmt.Errorf("compression method `%d` not supported", method)
	}

	return rewindAndHandleError(zippedFile, "zipping bundle failed with: %w", err)
}

func rewindAndHandleError(rsc io.ReadSeekCloser, errFormat string, err error) (io.ReadSeekCloser, error) {
	if err != nil {
		return nil, fmt.Errorf(errFormat, err)
	}

	if rsc != nil {
		if _, err = rsc.Seek(0, io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek to start failed with: %w", err)
		}

		return rsc, nil
	}

	return nil, fmt.Errorf("nil ReadSeekCloser")
}

func (o *output) handleDeprecated() (io.ReadSeekCloser, error) {
	compressedWasm, err := bundle.Compress(wasm.WasmOutput(o.outDir), o.wd.Wasm().WasmCompressed(), wasm.BufferSize)
	if err != nil {
		return nil, err
	}

	return rewindAndHandleError(compressedWasm, "compressing wasm failed with: %w", err)
}
