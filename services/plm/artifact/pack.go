package artifact

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/taubyte/tau/pkg/invoke/js/archive"
	"github.com/taubyte/tau/pkg/specs"
)

// The js tier reads a zip whose manifest names the entry module
// (pkg/invoke/js/archive), so a js bundle is packed into one with its source map.
// The compiled `.warp` form is not made yet: warpjs cannot compile an ES module.

// product is what a build in dir left behind for spec, as the tier that runs it
// reads it.
func product(dir string, spec Spec) (io.ReadCloser, error) {
	if spec.Type == specs.PackageTypeWarp {
		return packJS(dir, spec.Output)
	}
	return collect(dir, spec.Output)
}

// mapExt is where a bundler writes a bundle's source map: beside it, named
// after it.
const mapExt = ".map"

// packJS packs the bundle at output, and its source map if there is one, into a
// js archive whose entry is the bundle.
func packJS(dir, output string) (io.ReadCloser, error) {
	bundle := joinWithin(dir, output)
	entry := path.Base(filepath.ToSlash(bundle))

	files := map[string][]byte{}
	budget := int64(archive.DefaultMax)
	body, err := readProduct(bundle, output, &budget)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("artifact: the build left nothing at %q", output)
	}
	if err != nil {
		return nil, err
	}
	files[entry] = body

	sourceMap, err := readProduct(bundle+mapExt, output+mapExt, &budget)
	switch {
	case err == nil:
		files[entry+mapExt] = sourceMap
	case !os.IsNotExist(err):
		return nil, err
	}

	var packed bytes.Buffer
	if err := archive.Write(&packed, entry, files); err != nil {
		return nil, fmt.Errorf("artifact: packing %q: %w", output, err)
	}
	return io.NopCloser(&packed), nil
}

// readProduct reads one built file within budget, refusing a link.
func readProduct(file, named string, budget *int64) ([]byte, error) {
	info, err := os.Lstat(file)
	if os.IsNotExist(err) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("artifact: reading %q: %w", named, err)
	}
	switch {
	case info.IsDir():
		return nil, fmt.Errorf("artifact: the build left a directory at %q, and a js function's bundle and its source map are files", named)
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("artifact: the build left %q as a link or a device, and only a file it wrote is packed", named)
	case info.Size() > *budget:
		return nil, fmt.Errorf("artifact: %q makes the archive larger than the %d bytes the js tier reads", named, archive.DefaultMax)
	}

	body, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("artifact: reading %q: %w", named, err)
	}
	*budget -= int64(len(body))
	return body, nil
}
