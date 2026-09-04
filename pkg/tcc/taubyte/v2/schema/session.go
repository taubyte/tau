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

// bindings wires the generic session to THIS DSL: how to compile it, how to check one
// field of it without compiling, how its documents are laid out — which is also what
// says where a lowering's documents go — and how the values nobody authors are minted.
//
// The session's core is DSL-agnostic and every one of these is optional, which means an
// unset one is not an error anywhere: an unwired FieldValidator makes ValidateField
// accept whatever it is handed, and an unwired Generator makes Generate return "no
// generator wired". Both read as "this vocabulary has no rule" rather than as a missing
// binding, so both were silently true here.
//
// Completer is the one that stays unset, and deliberately — see v2FieldValidator.
var bindings = session.Bindings{
	CompilerFor:    v2CompilerFor,
	FieldValidator: v2FieldValidator{},
	Layout:         v2Layout{},
	Generator:      v2Generator{},
}

// v2FieldValidator runs this vocabulary's declared per-value checks — the same ones the
// compiler runs at load — against one field, with no compile. It is the live-editing
// path: what a CLI or an editor asks between keystrokes.
//
// NO COMPLETER IS WIRED, and that is what keeps this honest rather than an omission.
// Wiring one also switches on the session's reference check, which resolves a bare name
// against "the resource's own application, then the root" — v1's scope rule. THIS
// vocabulary's is scopeChain: a reference is a block, and the `namespace` beside the
// name is what says where to look, absolutely (`/billing`), relatively (`eu`) or by
// walking outward when it is absent. Reporting a RefGroup here made the session reject
// `authorizer: {name: tier_check, namespace: /shop}` in cache/orders.yaml — config that
// compiles — with `no function named "tier_check" in scope`. A check that rejects valid
// config is worse than no check, so the reference half waits until the session can be
// told the scope; completion of fixed values waits with it rather than shipping a field
// that quietly answers "no suggestions" for every reference in the vocabulary.
type v2FieldValidator struct{}

// ValidateField checks one value against the field's declared type and validator. Two
// things it does before asking the engine, both consequences of what THIS vocabulary
// declares rather than of anything generic:
//
// A KEY THAT IS BOTH A VALUE AND A BLOCK. `source` is a bare path in this repository OR
// one of the block forms (source.go), so a mapping there is not a string that came out
// wrong — it is the other branch, whose leaves are validated at their own paths. Passing
// the block to the scalar attribute called every `source: {component: paylib}` in the
// fixtures a bad string.
//
// A LIST ARRIVES AS []any. A yaml sequence decodes to []any and the compile path coerces
// it before any validator sees it; here the value comes straight off the document, so a
// validator declared over []string — EachInSet, on every method list — answered "invalid
// type passed to validator" for `methods: [POST]`. Same coercion, same place in the
// order, so the check that runs is the declared one.
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

// v2Generator mints the values nobody authors: a resource's id. It is the whole reason a
// consumer does not carry its own "how do I make an id" — and the two that did carry one
// disagreed about it.
type v2Generator struct{}

// GeneratedBy answers for a resource kind, and for THE PROJECT ITSELF.
//
// The project's `id` is an attribute of the compile root and not of any group,
// so a lookup by group name finds nothing for it — and a session addresses the
// root as one segment, which resGroup reports as no group at all. The effect
// was that Session.Generate could mint an id for every resource in a project
// and not for the project, so nothing could create one: the first thing a
// caller has to do was the one thing this could not answer.
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

// NewSession opens an editable session over the config under dir in fs, bound to this
// DSL. It is what `Lower` and `Lowered` are asked through: a caller that can validate a
// resource can ask what it becomes, with the same address and nothing new to learn.
func NewSession(fs afero.Fs, dir string) (*Session, error) { return session.New(fs, dir, bindings) }

// AdoptSession opens a session directly over fs (no copy), for a caller that already
// owns a private filesystem.
func AdoptSession(fs afero.Fs) (*Session, error) { return session.Adopt(fs, bindings) }

func v2CompilerFor(fs afero.Fs, branch, _ string) (*interp.Compiler, error) {
	opts := []Option{WithVirtual(fs, "/")}
	if branch != "" {
		opts = append(opts, WithBranch(branch))
	}
	return New(opts...)
}

// v2Layout tells the session how this DSL lays its documents out. Read off the live
// schema, so a new group needs no change here.
type v2Layout struct{}

func (v2Layout) ContainerDoc(group string) string {
	return engine.ContainerDoc(GenerationRoot(), group)
}

// RootDoc is what the SCHEMA says, not what the engine falls back to: this vocabulary
// declares Leaf("project"), so the root document is project.yaml. Answering "config"
// made Session.Root() name a file that does not exist, and docPath reject the one that
// does.
func (v2Layout) RootDoc() string { return engine.LeafOf(CompileRoot()) }

// ResourcePath and ScopePath are the same templates decompile writes by, which is what
// makes a lowering's documents land where an author would have put them.
func (v2Layout) ResourcePath() string { return LayoutOf().Resource }
func (v2Layout) ScopePath() string    { return LayoutOf().Scope }

// ContentPath is where a resource's authored content sits when `source` does not say —
// the base an agent's `instructions` is a path against. It travels with the other two
// because a consumer that has to resolve one of these has to resolve all of them, and
// a template only reachable from Go is a rule every other consumer reinvents.
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
