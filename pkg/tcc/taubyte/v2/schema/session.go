package schema

import (
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
)

// bindings wires the generic session to THIS DSL: how to compile it, and how its
// documents are laid out — which is also what says where a lowering's documents go.
var bindings = session.Bindings{
	CompilerFor: v2CompilerFor,
	Layout:      v2Layout{},
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
