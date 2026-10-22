package session

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"gotest.tools/v3/assert"
)

type fakeLayout struct{ identified bool }

func (fakeLayout) ContainerDoc(group string) string {
	if group == "apps" {
		return "config"
	}
	return ""
}
func (fakeLayout) RootDoc() string { return "project" }
func (fakeLayout) Kinds() []Kind {
	return []Kind{
		{Name: "app", Group: "apps", Container: true},
		{Name: "pipeline", Group: "pipeline"},
		{Name: "job", Group: "job"},
		{Name: "log", Group: "log"},
		{Group: "misc"},
	}
}

type identifiedLayout struct{ fakeLayout }

func (identifiedLayout) Identity() (string, string) { return "id", "name" }

type fakeValidator struct{}

func (fakeValidator) ValidateField(group string, field []string, value any) error {
	if group == "pipeline" && strings.Join(field, "/") == "mode" {
		if s, _ := value.(string); s != "fast" && s != "slow" {
			return fmt.Errorf("mode %v is not fast or slow", value)
		}
	}
	return nil
}

func (fakeValidator) Fields(group string) [][]string {
	if group == "pipeline" {
		return [][]string{{"mode"}, {"job"}, {"absent"}}
	}
	return nil
}

func (fakeValidator) RequiredFields(group string) []RequiredField {
	if group != "pipeline" {
		return nil
	}
	return []RequiredField{
		{FieldRef: FieldRef{Path: []string{"mode"}}},
		{FieldRef: FieldRef{Path: []string{"channel"}}, When: &FieldRef{Path: []string{"trigger"}}, WhenIn: []string{"pubsub"}},
		{
			FieldRef:     FieldRef{Path: []string{"object", "size"}, AnyOf: [][]string{{"object", "size"}, {"streaming", "size"}, {}}},
			WhenBranch:   true,
			WhenBranchAt: []string{},
			WhenIn:       []string{"object", "streaming"},
		},
		{FieldRef: FieldRef{Path: []string{"build", "commands", "run"}}, WhenBranch: true, WhenBranchAt: []string{"build"}, WhenIn: []string{"commands"}},
	}
}

type fakeCompleter struct{}

func (fakeCompleter) Field(group string, field []string) ([]string, string, bool) {
	switch strings.Join(field, "/") {
	case "mode":
		return []string{"fast", "slow"}, "", true
	case "job":
		return nil, "job", true
	case "note":
		return nil, "", true
	}
	return nil, "", false
}

type fakeGenerator struct{ seeds []any }

func (fakeGenerator) GeneratedBy(group string, field []string) string {
	if slices.Equal(field, []string{"id"}) {
		return "cid"
	}
	return ""
}

func (g *fakeGenerator) Generate(kind string, seed ...any) (string, error) {
	g.seeds = seed
	if kind != "cid" {
		return "", errors.New("unknown generator")
	}
	return fmt.Sprint(kind, seed), nil
}

// A vocabulary tcc has never heard of, with one lowering, compiled for real.
func pipelineCompiler(fs afero.Fs, opts CompileOptions) (*interp.Compiler, error) {
	root := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("job", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
		}, engine.Resource("job", "Job", "Job", "job"))),
		engine.DefineGroup("pipeline", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required()),
			engine.String("name"),
		},
			engine.Resource("pipeline", "Pipeline", "Pipeline", "pipeline"),
			interp.Lowers(interp.Into("job",
				interp.Named(interp.Concat(interp.Attr("name"), interp.Text("-run"))),
				interp.Carries(interp.Text("runs it")),
			)),
		)),
	).With(engine.Leaf("project"), engine.Identity("id", "name"))
	return interp.New(engine.SchemaDefinition(root), root, interp.WithVirtual(fs, "/"),
		interp.WithLayout(interp.Layout{Resource: "{kind}/{name}.yaml"}))
}

func fullBindings() Bindings {
	return Bindings{
		CompilerFor:    pipelineCompiler,
		FieldValidator: fakeValidator{},
		Completer:      fakeCompleter{},
		Layout:         identifiedLayout{},
		Generator:      &fakeGenerator{},
	}
}

func newSession(t *testing.T, bind Bindings, files map[string]string) *Session {
	t.Helper()
	src := afero.NewMemMapFs()
	for p, c := range files {
		assert.NilError(t, afero.WriteFile(src, p, []byte(c), 0o644))
	}
	s, err := New(src, "/", bind)
	assert.NilError(t, err)
	return s
}
