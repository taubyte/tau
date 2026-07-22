package schema

import (
	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/session"
)

// Session is the editable configuration session (pkg/tcc/session), re-exported so
// Go callers (e.g. tau-cli) depend only on this package. CompileOptions likewise.
type (
	Session        = session.Session
	CompileOptions = session.CompileOptions
)

// NewSession opens an editable session over the config under dir in fs, bound to
// THIS DSL's compiler. Edit via Get/Set/Delete/List, Validate/Compile the whole
// config, and Fork/Merge to validate speculative edits before adopting them —
// same abstraction the browser wasm exposes.
func NewSession(fs afero.Fs, dir string) (*Session, error) {
	return session.New(fs, dir, taubyteCompilerFor)
}

// AdoptSession opens a session directly over fs (no copy), bound to this DSL's
// compiler — for callers that already own a private filesystem.
func AdoptSession(fs afero.Fs) (*Session, error) {
	return session.Adopt(fs, taubyteCompilerFor)
}

// taubyteCompilerFor binds the generic session to this DSL's compiler.
func taubyteCompilerFor(fs afero.Fs, branch, cloud string) (*interp.Compiler, error) {
	opts := []Option{WithVirtual(fs, "/")}
	if branch != "" {
		opts = append(opts, WithBranch(branch))
	}
	if cloud != "" {
		opts = append(opts, WithCloud(cloud))
	}
	return New(opts...)
}
