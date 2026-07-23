package schema

import (
	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/session"
)

// Session is the editable configuration session (pkg/tcc/session), re-exported so
// Go callers (e.g. tau-cli) depend only on this package. CompileOptions likewise.
type (
	Session        = session.Session
	CompileOptions = session.CompileOptions
)

// bindings wires the generic session to THIS DSL: its compiler (whole-config) and
// its single-value field validators (partial, compile-free, for live editing).
var bindings = session.Bindings{
	CompilerFor:    taubyteCompilerFor,
	FieldValidator: taubyteFieldValidator{},
}

// NewSession opens an editable session over the config under dir in fs, bound to
// THIS DSL. Edit via Get/Set/Delete/List; Validate/Compile the whole config;
// ValidateField/ValidateResource for cheap per-field / per-file checks; Fork/Merge
// to validate speculative edits before adopting them — same abstraction the
// browser wasm exposes.
func NewSession(fs afero.Fs, dir string) (*Session, error) {
	return session.New(fs, dir, bindings)
}

// AdoptSession opens a session directly over fs (no copy), bound to this DSL — for
// callers that already own a private filesystem.
func AdoptSession(fs afero.Fs) (*Session, error) {
	return session.Adopt(fs, bindings)
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

// taubyteFieldValidator runs this DSL's single-value field validators against the
// live schema — the same checks the compiler runs at load, without compiling.
type taubyteFieldValidator struct{}

func (taubyteFieldValidator) ValidateField(group string, field []string, value any) error {
	return engine.ValidateField(GenerationRoot(), group, field, value)
}

func (taubyteFieldValidator) Fields(group string) [][]string {
	vfs := engine.ValidatedFields(GenerationRoot(), group)
	out := make([][]string, len(vfs))
	for i, vf := range vfs {
		out[i] = vf.Path
	}
	return out
}

// ValidateField runs this DSL's single-value validator for one field of a resource
// group, without a session — for direct callers (e.g. tau-cli). Same partial-
// validation semantics as Session.ValidateField.
func ValidateField(group string, field []string, value any) error {
	return engine.ValidateField(GenerationRoot(), group, field, value)
}
