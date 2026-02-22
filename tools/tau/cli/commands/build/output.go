package build

import (
	"fmt"
	"io"
	"os"
)

// writeCompressedToOutput copies the compressed stream to the output file. If outPath is empty,
// creates a temp file with the given pattern (e.g. "tau-build-*.wasm") and prints its path to stdout.
// Returns the path used and any error.
func writeCompressedToOutput(r io.Reader, outPath string, tempPattern string) (string, error) {
	var (
		f      *os.File
		err    error
		isTemp bool
	)
	if outPath != "" {
		f, err = os.Create(outPath)
		if err != nil {
			return "", fmt.Errorf("creating output file failed: %w", err)
		}
		defer f.Close()
	} else {
		f, err = os.CreateTemp("", tempPattern)
		if err != nil {
			return "", fmt.Errorf("creating temp file failed: %w", err)
		}
		defer f.Close()
		outPath = f.Name()
		isTemp = true
	}
	_, err = io.Copy(f, r)
	if err != nil {
		return outPath, fmt.Errorf("writing output failed: %w", err)
	}
	if isTemp {
		fmt.Fprintln(os.Stdout, outPath)
	}
	return outPath, nil
}
