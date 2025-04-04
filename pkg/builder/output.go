package builder

import (
	"fmt"
	"io"
	"os"

	"github.com/taubyte/tau/core/builders"
	spec "github.com/taubyte/tau/pkg/specs/builders"
	"github.com/taubyte/tau/pkg/specs/builders/wasm"
	"github.com/taubyte/utils/bundle"
)

var DeprecatedWasmBuild bool

/*
	TODO: build Flag
	const DeprecatedWasmBuild

*/

// new sets the working directory and log file of the desired output
func new(wd spec.Dir) (out *output, err error) {
	// set working
	out = &output{
		wd: wd,
	}

	// logs are set to a temporary directory
	logFile, err := os.CreateTemp("", "logs")
	if err != nil {
		return nil, fmt.Errorf("creating temp log file failed with: %w", err)
	}

	out.logs = logs{logFile}

	return
}

// deferHandler copies std to the output logs
func (o *output) deferHandler() {
	io.Copy(os.Stdout, o.logs)
	o.logs.Seek(0, io.SeekStart)
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
		zippedFile, err = bundle.Zip(wasm.WasmOutput(o.outDir), o.wd.Wasm().Zip(), bundle.ZipFile)
		if err != nil {
			zippedFile, err = bundle.Zip(wasm.WasmDeprecatedOutput(o.outDir), o.wd.Wasm().Zip(), bundle.ZipFile)
		}
	case builders.Website:
		zippedFile, err = bundle.Zip(o.outDir, o.wd.Website().BuildZip(), bundle.ZipDir)
	default:
		return nil, fmt.Errorf("compression method `%d` not supported", method)
	}

	return rewindAndHandleError(zippedFile, "zipping bundle failed with: %w", err)
}

// Close will Close logs
func (o *output) Close() error {
	if o.logs.File != nil {
		err := o.logs.Close()
		if err != nil {
			return fmt.Errorf("closing logs failed with: %w", err)
		}

		// according to doc, it's safe to call after close
		err = os.Remove(o.logs.File.Name())
		if err != nil {
			return fmt.Errorf("removing logs file failed with: %w", err)
		}
	}

	return nil
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
