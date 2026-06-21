package website

import (
	"os"
	"path/filepath"

	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

// SSRManifestPath returns the on-disk location of the SSR manifest within a
// build output directory. The manifest, together with the server bundle, is
// written by the framework adapter and rides inside the website build zip.
func SSRManifestPath(outDir string) string {
	return filepath.Join(outDir, filepath.FromSlash(websiteSpec.ManifestPath))
}

// SSRHandlerPath returns the on-disk location of the server bundle wasm archive
// within a build output directory.
func SSRHandlerPath(outDir string) string {
	return filepath.Join(outDir, filepath.FromSlash(websiteSpec.DefaultHandlerPath))
}

// IsSSROutput reports whether a build output directory is a server side
// rendered website, i.e. it carries an SSR manifest.
func IsSSROutput(outDir string) bool {
	info, err := os.Stat(SSRManifestPath(outDir))
	return err == nil && !info.IsDir()
}
