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

// pack.go is the step after the author's commands that is the platform's own.
//
// # A js function ships as an archive, not as its bundle
//
// The js tier reads a zip whose manifest names the entry module
// (pkg/invoke/js/archive), so the bundle a build wrote at `output:` is packed
// into one, with its source map when the build left one beside it. The
// author's build is a bundler run like any other; nothing in it is tau's.
//
// THE COMPILED FORM IS NOT MADE YET. warpjs cannot compile an ES module, so an
// archive holds the source and the tier loads it from there. Once it can, the
// `.warp` beside each module is made here, and a reader that cannot use one
// still runs the source.

// product is what a build in dir left behind for spec, as the tier that runs it
// reads it.
func product(dir string, spec Spec) (io.ReadCloser, error) {
	if spec.Runtime == specs.WorkloadBlockRuntimeJs {
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

// readProduct reads one file a build wrote, within what is left of budget.
//
// A LINK IS REFUSED rather than followed, as it is in a directory product: a
// build controls what it writes, and a bundle that is a link to a file of this
// machine's would put that file in the artifact.
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
