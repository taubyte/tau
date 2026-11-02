package schema

import (
	"slices"
	"strings"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/session"
)

// Session is the editable configuration session (pkg/tcc/session), re-exported so a Go
// caller — tau-cli, the console's server side — depends only on this package.
type (
	Session        = session.Session
	CompileOptions = session.CompileOptions
	Kind           = session.Kind
	FieldIssue     = session.FieldIssue
)

// bindings wires the generic session to this vocabulary: compile, compile-free
// field checks, document layout and id minting. Every binding is optional to the
// session, so an unwired one silently reads as "no rule". Completer stays unset;
// see v2FieldValidator.
var bindings = bindingsIn(nil)

// bindingsIn is the bindings with the Regions a session's views are cut by; nil is
// WithNoRegions' answer, which is one view of everything.
func bindingsIn(r Regions) session.Bindings {
	return session.Bindings{
		CompilerFor:    v2CompilerFor,
		FieldValidator: v2FieldValidator{},
		Layout:         v2Layout{},
		Generator:      v2Generator{},
		Viewer:         v2Viewer{regions: r},
	}
}

// v2Viewer is Views, as the session asks for it.
type v2Viewer struct{ regions Regions }

func (v v2Viewer) Views(flat map[string]any, environment string) ([]session.View, []session.Note, error) {
	views, findings, err := Views(flat, WithRegions(v.regions), AuditIn(environment))
	notes := make([]session.Note, 0, len(findings))
	for _, f := range findings {
		notes = append(notes, session.Note{Severity: string(f.Severity), Kind: f.Kind, Name: f.Name,
			Scope: f.Scope, ID: f.ID, Title: f.Title, Detail: f.Detail})
	}
	if err != nil {
		return nil, notes, err
	}
	out := make([]session.View, 0, len(views))
	for _, view := range views {
		roles := make(map[string]string, len(view.Roles))
		for id, role := range view.Roles {
			roles[id] = string(role)
		}
		out = append(out, session.View{Destination: view.Region, Object: view.Object, Roles: roles})
	}
	return out, notes, nil
}

// v2FieldValidator runs the vocabulary's per-value checks against one field with
// no compile, for live editing. No Completer is wired: that would switch on the
// session's v1-style reference check, which rejects valid scoped references like
// `authorizer: {name: tier_check, namespace: /shop}`.
type v2FieldValidator struct{}

// ValidateField checks one value against the field's declared type and validator.
// A mapping under a key that also has block forms (`source`) is that block,
// validated at its own leaves; a yaml sequence arrives as []any and is coerced
// like the compile path does before an []string validator sees it.
func (v v2FieldValidator) ValidateField(group string, field []string, value any) error {
	if _, isBlock := value.(map[string]any); isBlock && v.holdsBlock(group, field) {
		return nil
	}
	return engine.ValidateField(GenerationRoot(), group, field, stringList(value))
}

// holdsBlock reports whether the vocabulary declares fields BENEATH this one, which is
// what makes a mapping at it a block rather than a mistyped value. Read off the same
// field list partial validation walks, so a second key that grows a block form needs
// nothing here.
func (v v2FieldValidator) holdsBlock(group string, field []string) bool {
	for _, f := range v.Fields(group) {
		if len(f) <= len(field) {
			continue
		}
		if slices.Equal(f[:len(field)], field) {
			return true
		}
	}
	return false
}

// stringList gives a decoded yaml sequence the type its validator declares. A sequence
// holding anything else is left alone, so "array of strings" is still what the engine
// says about it.
func stringList(value any) any {
	raw, ok := value.([]any)
	if !ok {
		return value
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		s, ok := e.(string)
		if !ok {
			return value
		}
		out = append(out, s)
	}
	return out
}

func (v2FieldValidator) Fields(group string) [][]string {
	return engine.CheckFields(GenerationRoot(), group)
}

// RequiredFields projects the vocabulary's required fields — the conditional ones
// included — into the session's terms, so the session never has to look a discriminator
// up by name.
func (v2FieldValidator) RequiredFields(group string) []session.RequiredField {
	src := engine.RequiredFields(GenerationRoot(), group)
	out := make([]session.RequiredField, 0, len(src))
	for _, r := range src {
		rf := session.RequiredField{
			FieldRef:     session.FieldRef{Path: r.Path, AnyOf: r.AnyOf},
			WhenIn:       r.WhenIn,
			WhenBranch:   r.WhenBranch,
			WhenBranchAt: r.WhenBranchAt,
			In:           r.In,
			List:         r.List,
			Claimed:      r.Claimed,
		}
		if r.When != nil {
			rf.When = &session.FieldRef{Path: r.When.Path}
		}
		out = append(out, rf)
	}
	return out
}

// v2Generator mints the values nobody authors: a resource's id.
type v2Generator struct{}

// GeneratedBy answers for a resource kind and for the project itself, whose `id`
// is on the compile root rather than any group.
func (v2Generator) GeneratedBy(group string, field []string) string {
	if group == "" || group == engine.LeafOf(CompileRoot()) {
		return rootGeneratedBy(field)
	}
	return engine.GeneratedBy(GenerationRoot(), group, field)
}

// rootGeneratedBy reads a generated attribute off the compile root.
func rootGeneratedBy(field []string) string {
	if len(field) != 1 {
		return ""
	}
	for _, a := range CompileRoot().Attributes {
		if a.Name == field[0] {
			kind, _ := a.Meta["generated"].(string)
			return kind
		}
	}
	return ""
}

func (v2Generator) Generate(kind string, seed ...any) (string, error) {
	return engine.Generate(kind, seed...)
}

// NewSession opens an editable session over the config under dir in fs, bound to
// this vocabulary.
func NewSession(fs afero.Fs, dir string) (*Session, error) { return session.New(fs, dir, bindings) }

// NewSessionIn is NewSession whose views are cut by the Regions given — what a build
// serving those regions would publish.
func NewSessionIn(fs afero.Fs, dir string, r Regions) (*Session, error) {
	return session.New(fs, dir, bindingsIn(r))
}

// AdoptSession opens a session directly over fs (no copy), for a caller that already
// owns a private filesystem.
func AdoptSession(fs afero.Fs) (*Session, error) { return session.Adopt(fs, bindings) }

func v2CompilerFor(fs afero.Fs, o session.CompileOptions) (*interp.Compiler, error) {
	opts := []Option{WithVirtual(fs, "/")}
	if o.Branch != "" {
		opts = append(opts, WithBranch(o.Branch))
	}
	// THE WHOLE TABLE IS DECLARED, the way deploy compiles: one document carries every
	// deployment's spelling, so a suffix is a field and not a typo, bound or not.
	declared, err := EnvironmentsOf(fs, "/")
	if err != nil {
		return nil, err
	}
	if len(declared) > 0 {
		opts = append(opts, WithEnvironments(EnvironmentNames(declared)...))
	}
	if o.Environment != "" {
		opts = append(opts, WithEnvironment(o.Environment))
	}
	return New(opts...)
}

// v2Layout tells the session how this DSL lays its documents out. Read off the live
// schema, so a new group needs no change here.
type v2Layout struct{}

func (v2Layout) ContainerDoc(group string) string {
	return engine.ContainerDoc(GenerationRoot(), group)
}

// RootDoc is the schema's Leaf("project"), so the root document is project.yaml.
func (v2Layout) RootDoc() string { return engine.LeafOf(CompileRoot()) }

// ResourcePath and ScopePath are the same templates decompile writes by, which is what
// makes a lowering's documents land where an author would have put them.
func (v2Layout) ResourcePath() string { return LayoutOf().Resource }
func (v2Layout) ScopePath() string    { return LayoutOf().Scope }

// ContentPath is where a resource's authored content sits when `source` does not
// say, such as an agent's `instructions` base.
func (v2Layout) ContentPath() string { return LayoutOf().Content }

// Identity is what this vocabulary calls the two fields every resource carries, read off
// the schema that states it.
func (v2Layout) Identity() (string, string) { return engine.IdentityOf(CompileRoot()) }

func (v2Layout) Kinds() []session.Kind {
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
