package schema

import (
	"strings"

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
	FieldIssue     = session.FieldIssue
	Kind           = session.Kind
)

// bindings wires the generic session to THIS DSL: its compiler (whole-config),
// its single-value field validators (partial, compile-free, for live editing),
// its completion sources, its document layout, and its value generators.
var bindings = session.Bindings{
	CompilerFor:    taubyteCompilerFor,
	FieldValidator: taubyteFieldValidator{},
	Completer:      taubyteCompleter{},
	Layout:         taubyteLayout{},
	Generator:      taubyteGenerator{},
}

// taubyteLayout tells the session how this DSL lays its documents out: an
// application is a directory whose own fields live in config.yaml, everything
// else is one file. Read off the live schema, so a new container group in the
// DSL needs no change here.
type taubyteLayout struct{}

func (taubyteLayout) ContainerDoc(group string) string {
	return engine.ContainerDoc(GenerationRoot(), group)
}
func (taubyteLayout) RootDoc() string { return engine.NodeDefaultSeerLeaf }

// Kinds projects the DSL's groups into the session's kind vocabulary. The
// singular is lowercased so a consumer routing on a kind string ("function")
// matches without knowing it derives from a Go type name.
//
// A group the DSL never NAMES is not a kind: `clouds` is a leaf map of settings
// authored inside the root document (see cloudsGroup), not a directory of
// resources, and it declares neither Resource() nor Singular(). Reporting it
// would hand every consumer a nameless entry to filter out — the DSL already
// says it isn't one.
func (taubyteLayout) Kinds() []session.Kind {
	groups := engine.Groups(GenerationRoot())
	out := make([]session.Kind, 0, len(groups))
	for _, g := range groups {
		if g.Singular == "" {
			continue
		}
		out = append(out, session.Kind{
			Name:      strings.ToLower(g.Singular),
			Group:     g.Group,
			Container: g.Container,
		})
	}
	return out
}

// taubyteGenerator mints this DSL's generated field values (a resource id, a CID).
type taubyteGenerator struct{}

func (taubyteGenerator) GeneratedBy(group string, field []string) string {
	return engine.GeneratedBy(GenerationRoot(), group, field)
}
func (taubyteGenerator) Generate(kind string, seed ...any) (string, error) {
	return engine.Generate(kind, seed...)
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
	// Every partial-checkable field — single-value validators AND references —
	// so per-resource validation covers both (the session checks reference
	// existence against the in-scope config).
	return engine.CheckFields(GenerationRoot(), group)
}

// RequiredFields projects the DSL's required fields — conditional ones included
// — into the session's vocabulary, resolving each condition's discriminator to
// an authored path so the session never has to look one up by name.
func (taubyteFieldValidator) RequiredFields(group string) []session.RequiredField {
	src := engine.RequiredFields(GenerationRoot(), group)
	out := make([]session.RequiredField, 0, len(src))
	for _, r := range src {
		rf := session.RequiredField{
			FieldRef: session.FieldRef{Path: r.Path, Compat: r.Compat, AnyOf: r.AnyOf},
			WhenIn:   r.WhenIn,
			Unless:   r.Unless,
		}
		if r.When != nil {
			rf.When = &session.FieldRef{Path: r.When.Path, Compat: r.When.Compat}
		}
		out = append(out, rf)
	}
	return out
}

// ValidateField runs this DSL's single-value validator for one field of a resource
// group, without a session — for direct callers (e.g. tau-cli). Same partial-
// validation semantics as Session.ValidateField.
func ValidateField(group string, field []string, value any) error {
	return engine.ValidateField(GenerationRoot(), group, field, value)
}

// taubyteCompleter supplies this DSL's field completion sources (enum members,
// shape literals, and reference-group descriptors) from the live schema.
type taubyteCompleter struct{}

func (taubyteCompleter) Field(group string, field []string) (values []string, refGroup, refPrefix string, found bool) {
	fc, found := engine.Completion(GenerationRoot(), group, field)
	return fc.Values, fc.RefGroup, fc.RefPrefix, found
}
