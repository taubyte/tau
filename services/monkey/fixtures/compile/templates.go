package compile

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/otiai10/copy"
	wasmSpec "github.com/taubyte/tau/pkg/specs/builders/wasm"
)

// copyTemplateConfig copies the vendored <lang> "common" build scaffold
// (.taubyte/{build.sh,config.yaml} + go.mod) into destination — the
// fixture-local, network-free replacement for wasmSpec.CopyFunctionTemplateConfig,
// which copied it out of a freshly-pulled tb_templates clone. The scaffold lives
// next to this source (assets/templates/), located via runtime.Caller so it
// resolves regardless of the calling test's working directory.
//
// The Go module file is vendored as go.mod.tmpl so it isn't seen as a nested
// module in the tau tree; it's renamed back to go.mod in the build dir.
func copyTemplateConfig(lang wasmSpec.SupportedLanguage, destination string) error {
	_, self, _, _ := runtime.Caller(0)
	src := filepath.Join(filepath.Dir(self), "assets", "templates", "code", "functions", string(lang), "common")

	if err := copy.Copy(src, destination); err != nil {
		return err
	}

	tmpl := filepath.Join(destination, "go.mod.tmpl")
	if _, err := os.Stat(tmpl); err == nil {
		return os.Rename(tmpl, filepath.Join(destination, "go.mod"))
	}
	return nil
}
