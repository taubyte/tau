package jobs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	commonSpec "github.com/taubyte/tau/pkg/specs/builders/common"
	"github.com/taubyte/tau/pkg/specs/builders/frameworks"
)

// ensureFrameworkBuildConfig gives zero-config hosting to popular JavaScript
// frameworks. When a website repository ships no Taubyte build configuration it
// detects the framework (Next.js, Nuxt, SvelteKit, Vite, Express, ...) and
// materialises a build config plus build script so the standard container
// builder can take over—producing static assets and, for SSR frameworks, the
// server bundle and manifest the runtime serves.
//
// Repositories that already carry a `.taubyte` (or legacy `taubyte`) directory
// are left untouched, so hand written build configuration always wins.
func ensureFrameworkBuildConfig(workDir string, log io.Writer) error {
	if hasTaubyteConfig(workDir) {
		return nil
	}

	fw, err := frameworks.DetectDir(workDir)
	if err != nil {
		// Not a recognised framework: let the builder surface the missing
		// configuration error exactly as it did before.
		return nil
	}

	gen, err := frameworks.Generate(fw)
	if err != nil {
		return fmt.Errorf("generating build config for %s failed with: %w", fw.Title, err)
	}

	fmt.Fprintf(log, "No Taubyte build config found; detected %s, generating one (render=%s)\n", fw.Title, fw.Mode)

	return frameworks.Materialize(workDir, gen)
}

// hasTaubyteConfig reports whether the working directory already provides a
// Taubyte build configuration directory.
func hasTaubyteConfig(workDir string) bool {
	for _, dir := range []string{commonSpec.TaubyteDir, commonSpec.DepreciatedTaubyteDir} {
		if info, err := os.Stat(filepath.Join(workDir, dir)); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}
