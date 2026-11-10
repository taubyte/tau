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

// archive is a build's compressed output. It lives in a directory of its own,
// never under the source directory: the build controls that directory, and a
// link it leaves there would redirect the write anywhere on the node.
// Closing it removes the archive and the build's output directory; nothing
// else does, and a node builds all day.
type archive struct {
	*os.File
	dir    string
	outDir string
}

func (a *archive) Close() error {
	err := a.File.Close()
	os.RemoveAll(a.dir)
	if a.outDir != "" {
		os.RemoveAll(a.outDir)
	}
	return err
}

// newArchivePath returns a path for the archive in a fresh private directory.
func newArchivePath(name string) (dir, path string, err error) {
	if dir, err = os.MkdirTemp("", "tau-build-*"); err != nil {
		return "", "", fmt.Errorf("creating archive directory failed with: %w", err)
	}
	return dir, filepath.Join(dir, name), nil
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
	switch method {
	case builders.WASM:
		if DeprecatedWasmBuild {
			return o.handleDeprecated()
		}

		dir, target, err := newArchivePath(filepath.Base(o.wd.Wasm().Zip()))
		if err != nil {
			return nil, err
		}

		// Try for both artifact/main.wasm
		zippedFile, err := o.zipOutput(wasm.WasmOutput(o.outDir), target)
		if err != nil {
			zippedFile, err = o.zipOutput(wasm.WasmDeprecatedOutput(o.outDir), target)
		}
		return o.finish(dir, zippedFile, "zipping bundle failed with: %w", err)
	case builders.Website:
		noFiles, err := outputDirHasNoFiles(o.outDir)
		if err != nil {
			o.dropOutDir()
			return nil, fmt.Errorf("checking website output directory failed with: %w", err)
		}
		if noFiles {
			o.dropOutDir()
			return nil, fmt.Errorf("website build produced no output: output directory is empty")
		}

		dir, target, err := newArchivePath(filepath.Base(o.wd.Website().BuildZip()))
		if err != nil {
			return nil, err
		}

		zippedFile, err := bundle.Zip(bundle.ZipDir, o.outDir, target)
		return o.finish(dir, zippedFile, "zipping bundle failed with: %w", err)
	default:
		return nil, fmt.Errorf("compression method `%d` not supported", method)
	}
}

// finish wraps a written archive so closing it cleans up, or removes the
// archive directory when writing it failed.
func (o *output) finish(dir string, file *os.File, errFormat string, err error) (io.ReadSeekCloser, error) {
	if err != nil || file == nil {
		if file != nil {
			file.Close()
		}
		os.RemoveAll(dir)
		o.dropOutDir()
		if err == nil {
			err = fmt.Errorf("nil ReadSeekCloser")
		}
		return nil, fmt.Errorf(errFormat, err)
	}

	a := &archive{File: file, dir: dir, outDir: o.outDir}
	rsc, err := rewindAndHandleError(a, errFormat, nil)
	if err != nil {
		a.Close()
	}
	return rsc, err
}

// dropOutDir removes the build output once nothing can use it.
func (o *output) dropOutDir() {
	if o.outDir != "" {
		os.RemoveAll(o.outDir)
	}
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
	dir, target, err := newArchivePath(filepath.Base(o.wd.Wasm().WasmCompressed()))
	if err != nil {
		return nil, err
	}

	compressedWasm, err := bundle.Compress(wasm.WasmOutput(o.outDir), target, wasm.BufferSize)
	return o.finish(dir, compressedWasm, "compressing wasm failed with: %w", err)
}
