package session

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"gotest.tools/v3/assert"
)

func TestDocPathAddresses(t *testing.T) {
	s := newSession(t, fullBindings(), nil)
	cases := []struct {
		res  []string
		want []string
		err  string
	}{
		{res: nil, err: "empty resource address"},
		{res: []string{"project"}, want: []string{"project"}},
		{res: []string{"pipeline"}, err: "a group and a name"},
		{res: []string{"apps", "a"}, want: []string{"apps", "a", "config"}},
		{res: []string{"apps", "a", "config"}, want: []string{"apps", "a", "config"}},
		{res: []string{"apps", "a", "other"}, err: "three deep"},
		{res: []string{"apps", "a", "apps", "b"}, want: []string{"apps", "a", "apps", "b", "config"}},
		{res: []string{"apps", "a", "pipeline", "p"}, want: []string{"apps", "a", "pipeline", "p"}},
		{res: []string{"pipeline", "p"}, want: []string{"pipeline", "p"}},
		{res: []string{"apps"}, err: "a group and a name"},
		{res: []string{"pipeline", "p", "x"}, err: "inside as many scopes"},
		{res: []string{"pipeline", "p", "apps"}, err: "inside as many scopes"},
	}
	for _, c := range cases {
		got, err := s.docPath(c.res)
		if c.err != "" {
			assert.ErrorContains(t, err, c.err, "%v", c.res)
			continue
		}
		assert.NilError(t, err, "%v", c.res)
		assert.DeepEqual(t, got, c.want)
	}

	plain := newSession(t, Bindings{}, nil)
	got, err := plain.docPath([]string{"anything", "at", "all"})
	assert.NilError(t, err)
	assert.DeepEqual(t, got, []string{"anything", "at", "all"})
}

func TestDocPathScopeAtResourceDepth(t *testing.T) {
	s := newSession(t, fullBindings(), nil)
	_, err := s.docPath([]string{"pipeline", "p", "apps", "x"})
	assert.ErrorContains(t, err, "inside as many scopes")
	_, err = s.docPath([]string{"apps", "a", "x", "apps", "b"})
	assert.ErrorContains(t, err, "inside as many scopes")
}

func TestGetSetDeleteAndErrors(t *testing.T) {
	s := newSession(t, fullBindings(), map[string]string{
		"/project.yaml":           "id: root\n",
		"/apps/a/config.yaml":     "id: appa\n",
		"/apps/a/pipeline/p.yaml": "id: pa\n",
		"/pipeline/build.yaml":    "id: one\nname: build\n",
	})

	v, err := s.Get([]string{"apps", "a"}, []string{"id"})
	assert.NilError(t, err)
	assert.Equal(t, v, "appa")

	_, err = s.Get([]string{"pipeline"}, []string{"id"})
	assert.ErrorContains(t, err, "not a resource address")
	assert.ErrorContains(t, s.Set([]string{"pipeline"}, []string{"id"}, "x"), "not a resource address")
	assert.ErrorContains(t, s.Delete([]string{"pipeline"}, []string{"id"}), "not a resource address")
	assert.ErrorContains(t, s.Delete([]string{"pipeline"}, nil), "not a resource address")

	assert.NilError(t, s.Set([]string{"pipeline", "build"}, []string{"mode"}, "fast"))
	assert.NilError(t, s.Delete([]string{"pipeline", "build"}, []string{"mode"}))
	_, err = s.Get([]string{"pipeline", "build"}, []string{"mode"})
	assert.Assert(t, err != nil, "deleted field must not read")

	assert.NilError(t, s.Delete([]string{"apps", "a"}, nil))
	assert.Assert(t, !s.Exists([]string{"apps", "a"}))
	_, err = s.FS().Stat("/apps/a/pipeline/p.yaml")
	assert.Assert(t, err != nil, "deleting a container removes what it held")
}

func TestExists(t *testing.T) {
	s := newSession(t, fullBindings(), map[string]string{
		"/project.yaml":        "id: root\n",
		"/pipeline/build.yaml": "id: one\n",
	})
	assert.Assert(t, s.Exists([]string{"project"}))
	assert.Assert(t, s.Exists([]string{"pipeline", "build"}))
	assert.Assert(t, !s.Exists([]string{"pipeline", "nope"}))
	assert.Assert(t, !s.Exists([]string{"job", "nope"}), "absent group directory")
	assert.Assert(t, !s.Exists([]string{"pipeline"}), "not an address")

	bare := newSession(t, fullBindings(), nil)
	assert.Assert(t, !bare.Exists([]string{"project"}))
}

func TestKindsAddressNamesRoot(t *testing.T) {
	s := newSession(t, fullBindings(), map[string]string{
		"/pipeline/build.yaml":    "id: one\n",
		"/apps/a/config.yaml":     "id: appa\n",
		"/apps/a/pipeline/p.yaml": "id: pa\n",
	})
	assert.Equal(t, len(s.Kinds()), 5)
	assert.DeepEqual(t, s.Root(), []string{"project"})

	addr, err := s.Address("Pipeline", "x", "")
	assert.NilError(t, err)
	assert.DeepEqual(t, addr, []string{"pipeline", "x"})
	addr, err = s.Address("apps", "a", "")
	assert.NilError(t, err)
	assert.DeepEqual(t, addr, []string{"apps", "a"})
	addr, err = s.Address("job", "j", "shop/local")
	assert.NilError(t, err)
	assert.DeepEqual(t, addr, []string{"apps", "shop", "apps", "local", "job", "j"})

	_, err = s.Address("nope", "x", "")
	assert.ErrorContains(t, err, `unknown kind "nope" (have apps, pipeline, job, log, misc)`)
	_, err = s.Address("job", "", "")
	assert.ErrorContains(t, err, "needs a name")
	_, err = s.Address("app", "a", "b")
	assert.ErrorContains(t, err, "container kind")
	_, err = s.Address("job", "j", "shop//x")
	assert.ErrorContains(t, err, "not a scope path")

	names, err := s.Names("pipeline", "")
	assert.NilError(t, err)
	assert.DeepEqual(t, names, []string{"build"})
	names, err = s.Names("pipeline", "a")
	assert.NilError(t, err)
	assert.DeepEqual(t, names, []string{"p"})
	names, err = s.Names("job", "")
	assert.NilError(t, err)
	assert.DeepEqual(t, names, []string{})
	_, err = s.Names("apps", "a")
	assert.ErrorContains(t, err, "container kind")
	_, err = s.Names("nope", "")
	assert.ErrorContains(t, err, "unknown kind")

	none := newSession(t, Bindings{}, nil)
	assert.Assert(t, none.Kinds() == nil)
	assert.Assert(t, none.Root() == nil)
	_, err = none.Address("job", "x", "")
	assert.ErrorContains(t, err, "no layout wired")
}

type flatLayout struct{}

func (flatLayout) ContainerDoc(string) string { return "" }
func (flatLayout) RootDoc() string            { return "root" }
func (flatLayout) Kinds() []Kind              { return []Kind{{Name: "job", Group: "job"}} }

func TestNoContainerKind(t *testing.T) {
	s := newSession(t, Bindings{Layout: flatLayout{}}, nil)
	_, err := s.Address("job", "x", "app")
	assert.ErrorContains(t, err, "no container kind")
	_, err = s.Names("job", "app")
	assert.ErrorContains(t, err, "no container kind")
	_, _, _, err = s.resourceAt([]string{"job", "x"})
	assert.NilError(t, err)
}

func TestResourceAt(t *testing.T) {
	s := newSession(t, fullBindings(), nil)
	cases := []struct {
		path string
		want []string
		ok   bool
	}{
		{"project.yaml", []string{"project"}, true},
		{"/other.yaml", []string{"other"}, false},
		{"pipeline/x.yml", nil, false},
		{"pipeline//x.yaml", nil, false},
		{"unknown/x.yaml", nil, false},
		{"pipeline/x.yaml", []string{"pipeline", "x"}, true},
		{"apps/a/config.yaml", []string{"apps", "a"}, true},
		{"apps/a/other.yaml", []string{"apps", "a"}, false},
		{"apps/a/pipeline/x.yaml", []string{"apps", "a", "pipeline", "x"}, true},
		{"apps/a/apps/b/job/j.yaml", []string{"apps", "a", "apps", "b", "job", "j"}, true},
		{"apps/a/apps/b.yaml", nil, false},
		{"apps/a/nope/x.yaml", []string{"apps", "a", "nope", "x"}, false},
		{"pipeline/x/y.yaml", nil, false},
	}
	for _, c := range cases {
		got, ok := s.ResourceAt(c.path)
		assert.Equal(t, ok, c.ok, c.path)
		if c.want != nil {
			assert.DeepEqual(t, got, c.want)
		}
	}
	none := newSession(t, Bindings{}, nil)
	_, ok := none.ResourceAt("project.yaml")
	assert.Assert(t, !ok)
}

func TestResourceAtInvertsLocation(t *testing.T) {
	s := newSession(t, fullBindings(), map[string]string{
		"/apps/x/config.yaml":        "id: x\n",
		"/apps/x/apps/y/config.yaml": "id: y\n",
		"/apps/x/pipeline/p.yaml":    "id: p\n",
		"/pipeline/q.yaml":           "id: q\n",
		"/project.yaml":              "id: root\n",
	})
	for _, res := range [][]string{{"apps", "x"}, {"apps", "x", "apps", "y"}, {"apps", "x", "pipeline", "p"}, {"pipeline", "q"}, {"project"}} {
		loc, err := s.Location(res)
		assert.NilError(t, err)
		got, ok := s.ResourceAt(loc)
		assert.Assert(t, ok, loc)
		assert.DeepEqual(t, got, res)
	}
	_, ok := s.ResourceAt("apps/x.yaml")
	assert.Assert(t, !ok)
}

func TestLocationAndSerialize(t *testing.T) {
	s := newSession(t, fullBindings(), map[string]string{
		"/pipeline/build.yaml": "# kept\nid: one\n",
		"/apps/a/config.yaml":  "id: appa\n",
	})
	p, err := s.Location([]string{"apps", "a"})
	assert.NilError(t, err)
	assert.Equal(t, p, "apps/a/config.yaml")

	assert.NilError(t, s.Set([]string{"pipeline", "build"}, []string{"name"}, "build"))
	p, data, err := s.Serialize([]string{"pipeline", "build"})
	assert.NilError(t, err)
	assert.Equal(t, p, "pipeline/build.yaml")
	assert.Assert(t, strings.Contains(string(data), "# kept"))
	assert.Assert(t, strings.Contains(string(data), "name: build"))

	_, err = s.Location([]string{"pipeline"})
	assert.ErrorContains(t, err, "not a resource address")
	_, _, err = s.Serialize([]string{"pipeline"})
	assert.ErrorContains(t, err, "not a resource address")
	_, err = s.Location([]string{"pipeline", "ghost"})
	assert.ErrorContains(t, err, "no document")
}

func TestValidateFieldAndResource(t *testing.T) {
	s := newSession(t, fullBindings(), map[string]string{
		"/job/j1.yaml":            "id: j1\n",
		"/apps/a/job/j2.yaml":     "id: j2\n",
		"/apps/b/job/j3.yaml":     "id: j3\n",
		"/pipeline/ok.yaml":       "mode: fast\njob: j1\n",
		"/pipeline/bad.yaml":      "mode: medium\njob: ghost\ntrigger: pubsub\nstreaming: {}\nbuild:\n  commands: {}\n",
		"/pipeline/empty.yaml":    "mode: ''\ntrigger: 3\nobject:\n  size: 1\n",
		"/apps/a/pipeline/p.yaml": "mode: slow\njob: [j1, j2, 7, '']\n",
	})

	assert.NilError(t, s.ValidateField([]string{"pipeline", "ok"}, []string{"mode"}, "slow"))
	assert.ErrorContains(t, s.ValidateField([]string{"pipeline", "ok"}, []string{"mode"}, "x"), "not fast or slow")
	assert.ErrorContains(t, s.ValidateField([]string{"pipeline", "ok"}, []string{"job"}, []string{"j3"}), `no job named "j3" in scope`)
	assert.NilError(t, s.ValidateField([]string{"pipeline", "ok"}, []string{"job"}, 42))

	assert.Equal(t, len(s.ValidateResource([]string{"pipeline", "ok"})), 0)
	assert.Equal(t, len(s.ValidateResource([]string{"apps", "a", "pipeline", "p"})), 0)

	got := map[string]string{}
	for _, i := range s.ValidateResource([]string{"pipeline", "bad"}) {
		got[strings.Join(i.Field, "/")] = i.Message
	}
	assert.DeepEqual(t, got, map[string]string{
		"channel":            "channel is required when trigger is pubsub",
		"streaming/size":     `required field "streaming/size" is missing`,
		"build/commands/run": `required field "build/commands/run" is missing`,
		"mode":               "mode medium is not fast or slow",
		"job":                `no job named "ghost" in scope`,
	})

	var empty []string
	for _, i := range s.ValidateResource([]string{"pipeline", "empty"}) {
		empty = append(empty, strings.Join(i.Field, "/")+": "+i.Message)
	}
	assert.DeepEqual(t, empty, []string{
		`mode: required field "mode" is missing`,
		"mode: mode  is not fast or slow",
	})

	none := newSession(t, Bindings{}, nil)
	assert.Assert(t, none.ValidateResource([]string{"pipeline", "ok"}) == nil)
	assert.NilError(t, none.ValidateField([]string{"pipeline", "ok"}, []string{"mode"}, "x"))
}

func TestAuthoredAtFallsBack(t *testing.T) {
	s := newSession(t, fullBindings(), map[string]string{"/pipeline/p.yaml": "id: x\n"})
	f := FieldRef{Path: []string{"object", "size"}, AnyOf: [][]string{{"object", "size"}, {"x"}}}
	assert.DeepEqual(t, s.authoredAt([]string{"pipeline", "p"}, f), []string{"object", "size"})
	assert.Assert(t, !s.branchIn([]string{"pipeline", "p"}, []string{"id"}, []string{"x"}), "a scalar is no branch")
}

func TestIsBlank(t *testing.T) {
	assert.Assert(t, isBlank(nil))
	assert.Assert(t, isBlank(""))
	assert.Assert(t, isBlank([]string{}))
	assert.Assert(t, isBlank([]any{}))
	assert.Assert(t, !isBlank(false))
	assert.Assert(t, !isBlank(0))
	assert.Assert(t, !isBlank([]string{"a"}))
}

func TestComplete(t *testing.T) {
	s := newSession(t, fullBindings(), map[string]string{
		"/job/Alpha.yaml":       "id: a\n",
		"/job/beta.yaml":        "id: b\n",
		"/apps/a/job/al.yaml":   "id: c\n",
		"/apps/a/job/beta.yaml": "id: d\n",
	})
	got, err := s.Complete([]string{"pipeline", "p"}, []string{"mode"}, "F")
	assert.NilError(t, err)
	assert.DeepEqual(t, got, []string{"fast"})

	got, err = s.Complete([]string{"apps", "a", "pipeline", "p"}, []string{"job"}, "")
	assert.NilError(t, err)
	sort.Strings(got)
	assert.DeepEqual(t, got, []string{"Alpha", "al", "beta"})

	got, err = s.Complete([]string{"apps", "a", "pipeline", "p"}, []string{"job"}, "al")
	assert.NilError(t, err)
	sort.Strings(got)
	assert.DeepEqual(t, got, []string{"Alpha", "al"})

	got, err = s.Complete([]string{"pipeline", "p"}, []string{"note"}, "")
	assert.NilError(t, err)
	assert.DeepEqual(t, got, []string{})

	_, err = s.Complete([]string{"pipeline", "p"}, []string{"typo"}, "")
	assert.ErrorContains(t, err, `unknown field "typo" on "pipeline"`)

	none := newSession(t, Bindings{}, nil)
	got, err = none.Complete([]string{"pipeline", "p"}, []string{"mode"}, "")
	assert.NilError(t, err)
	assert.Assert(t, got == nil)
}

func TestResGroup(t *testing.T) {
	s := newSession(t, fullBindings(), nil)
	assert.Equal(t, s.resGroup([]string{"apps", "a", "config"}), "apps")
	assert.Equal(t, s.resGroup([]string{"apps", "a", "job", "j"}), "job")
	assert.Equal(t, s.resGroup([]string{"project"}), "")
}

func TestGenerate(t *testing.T) {
	gen := &fakeGenerator{}
	b := fullBindings()
	b.Generator = gen
	s := newSession(t, b, nil)

	v, err := s.Generate([]string{"job", "j"}, []string{"id"}, "salt")
	assert.NilError(t, err)
	assert.Equal(t, v, "cid[salt job/j]")
	assert.DeepEqual(t, gen.seeds, []any{"salt", "job/j"})

	_, err = s.Generate([]string{"job", "j"}, []string{"name"})
	assert.ErrorContains(t, err, `"name" is not a generated field`)
	_, err = s.Generate([]string{"job"}, []string{"id"})
	assert.ErrorContains(t, err, "not a resource address")

	none := newSession(t, Bindings{}, nil)
	_, err = none.Generate([]string{"job", "j"}, []string{"id"})
	assert.ErrorContains(t, err, "no generator wired")
}

func TestSetResource(t *testing.T) {
	s := newSession(t, fullBindings(), map[string]string{
		"/pipeline/p.yaml": "# head\nid: one\nmode: fast\nold: gone\ntags: [a, b]\nnested:\n  keep: 1\n  drop: 2\n",
	})
	res := []string{"pipeline", "p"}
	assert.NilError(t, s.SetResource(res, map[string]any{
		"id":     "one",
		"mode":   "slow",
		"tags":   []any{"a", "b"},
		"nested": map[string]any{"keep": 1, "add": "x"},
		"none":   nil,
	}))
	got, err := s.Get(res, nil)
	assert.NilError(t, err)
	m := got.(map[string]any)
	assert.Equal(t, m["mode"], "slow")
	_, has := m["old"]
	assert.Assert(t, !has)
	nested := m["nested"].(map[string]any)
	_, has = nested["drop"]
	assert.Assert(t, !has)
	assert.Equal(t, nested["add"], "x")
	v, present := m["none"]
	assert.Assert(t, present && v == nil, "an explicit nil writes a null")

	_, data, err := s.Serialize(res)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(data), "# head"))

	before := string(data)
	assert.NilError(t, s.SetResource(res, m), "no diff is no write")
	_, data, err = s.Serialize(res)
	assert.NilError(t, err)
	assert.Equal(t, string(data), before)

	assert.NilError(t, s.SetResource([]string{"pipeline", "fresh"}, map[string]any{"id": "f"}))
	v, err = s.Get([]string{"pipeline", "fresh"}, []string{"id"})
	assert.NilError(t, err)
	assert.Equal(t, v, "f")

	assert.ErrorContains(t, s.SetResource([]string{"pipeline"}, map[string]any{"id": "x"}), "not a resource address")
}

func TestSetResourceFailureLeavesParentUntouched(t *testing.T) {
	s := newSession(t, fullBindings(), map[string]string{"/pipeline/p.yaml": "id: one\nmode: fast\ntags: [a]\n"})
	err := s.SetResource([]string{"pipeline", "p"}, map[string]any{"id": "one", "mode": "slow", "tags": map[string]any{"x": 1}})
	assert.Assert(t, err != nil, "setting a key under a list must fail the write")
	v, err := s.Get([]string{"pipeline", "p"}, []string{"mode"})
	assert.NilError(t, err)
	assert.Equal(t, v, "fast")
}

func TestSameValue(t *testing.T) {
	assert.Assert(t, sameValue([]string{"a"}, []any{"a"}))
	assert.Assert(t, !sameValue([]string{"a"}, []any{"b"}))
	assert.Assert(t, !sameValue([]string{"a"}, "a"))
	assert.Assert(t, !sameValue([]string{"a"}, []string{"a", "b"}))
	assert.Assert(t, sameValue(1, 1))
	assert.Assert(t, !sameValue(1, 2))
}

func TestSaveAndMergeNotAFork(t *testing.T) {
	s := newSession(t, fullBindings(), map[string]string{"/pipeline/p.yaml": "id: one\n"})
	assert.ErrorContains(t, s.Merge(), "not a fork")
	assert.NilError(t, s.Set([]string{"pipeline", "p"}, []string{"name"}, "p"))

	dst := afero.NewMemMapFs()
	assert.NilError(t, s.Save(dst, "/out"))
	data, err := afero.ReadFile(dst, "/out/pipeline/p.yaml")
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(data), "name: p"))

	s.Close()
	assert.Assert(t, s.seer == nil)
}

func TestNewFromSubdirAndMissing(t *testing.T) {
	src := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(src, "/repo/pipeline/p.yaml", []byte("id: one\n"), 0o644))
	s, err := New(src, "/repo", fullBindings())
	assert.NilError(t, err)
	assert.Assert(t, s.Exists([]string{"pipeline", "p"}))

	_, err = New(src, "/missing", fullBindings())
	assert.Assert(t, err != nil)

	assert.NilError(t, copyTree(src, "", afero.NewMemMapFs(), "/x"))
}

type unreadableFs struct{ afero.Fs }

func (u unreadableFs) Open(name string) (afero.File, error) {
	if strings.HasSuffix(name, ".yaml") {
		return nil, errors.New("unreadable")
	}
	return u.Fs.Open(name)
}

func TestCopyTreeErrors(t *testing.T) {
	src := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(src, "/a.yaml", []byte("x"), 0o644))
	assert.ErrorContains(t, copyTree(unreadableFs{src}, "/", afero.NewMemMapFs(), "/"), "unreadable")
	assert.Assert(t, copyTree(src, "/", afero.NewReadOnlyFs(afero.NewMemMapFs()), "/") != nil)
}

func TestCompileAuthoredValidate(t *testing.T) {
	s := newSession(t, fullBindings(), map[string]string{
		"/project.yaml":        "id: root\n",
		"/pipeline/build.yaml": "id: one\nname: build\n",
	})
	ctx := context.Background()

	obj, _, err := s.Compile(ctx, CompileOptions{})
	assert.NilError(t, err)
	jobs := obj.Flat()[interp.ProjectKey].(map[string]any)["job"].(map[string]any)
	assert.Equal(t, len(jobs), 1, "compile lowers the pipeline into its job")

	authored, _, err := s.Authored(ctx, CompileOptions{})
	assert.NilError(t, err)
	_, hasJobs := authored.Flat()[interp.ProjectKey].(map[string]any)["job"]
	assert.Assert(t, !hasJobs, "authored is what was written")

	_, err = s.Validate(ctx, CompileOptions{})
	assert.NilError(t, err)

	broken := newSession(t, Bindings{CompilerFor: noCompiler}, nil)
	_, _, err = broken.Compile(ctx, CompileOptions{})
	assert.ErrorContains(t, err, "compiler not needed")
	_, _, err = broken.Authored(ctx, CompileOptions{})
	assert.ErrorContains(t, err, "compiler not needed")
	_, err = broken.Views(ctx, CompileOptions{})
	assert.ErrorContains(t, err, "compiler not needed")
	_, _, _, err = broken.Became(ctx, []string{"pipeline", "x"}, CompileOptions{})
	assert.ErrorContains(t, err, "no layout wired")
}

func TestBecame(t *testing.T) {
	s := newSession(t, fullBindings(), map[string]string{
		"/project.yaml":        "id: root\n",
		"/pipeline/build.yaml": "id: one\nname: build\n",
	})
	ctx := context.Background()

	obj, made, added, err := s.Became(ctx, []string{"pipeline", "build"}, CompileOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(made), 1)
	assert.Equal(t, len(added), 0)
	jobs := obj.Flat()[interp.ProjectKey].(map[string]any)["job"].(map[string]any)
	_, there := jobs[made[0]]
	assert.Assert(t, there)

	_, made, _, err = s.Became(ctx, []string{"job", made[0]}, CompileOptions{})
	assert.ErrorContains(t, err, "is not in the compiled configuration", "a job has no name field in this vocabulary")
	assert.Assert(t, made == nil)

	_, _, _, err = s.Became(ctx, []string{"log", "x"}, CompileOptions{})
	assert.ErrorContains(t, err, "log/x is not in the compiled configuration")
	_, _, _, err = s.Became(ctx, []string{"pipeline"}, CompileOptions{})
	assert.ErrorContains(t, err, "not a resource address")
	_, _, _, err = s.Became(ctx, []string{"nope", "x"}, CompileOptions{})
	assert.ErrorContains(t, err, "unknown kind")
	_, _, _, err = s.Became(ctx, []string{"apps", "a", "apps"}, CompileOptions{})
	assert.ErrorContains(t, err, "not a resource address")

	kind, name, scope, err := s.resourceAt([]string{"apps", "a", "apps", "b", "job", "j"})
	assert.NilError(t, err)
	assert.Equal(t, kind+"|"+name+"|"+scope, "job|j|a/b")

	b := fullBindings()
	b.Layout = fakeLayout{}
	unnamed := newSession(t, b, map[string]string{
		"/project.yaml":        "id: root\n",
		"/pipeline/build.yaml": "id: one\nname: build\n",
	})
	_, _, _, err = unnamed.Became(ctx, []string{"pipeline", "build"}, CompileOptions{})
	assert.ErrorContains(t, err, "is not in the compiled configuration", "no identity, nothing found by name")

	b.Layout = identifiedLayout{}
	b.CompilerFor = func(afero.Fs, CompileOptions) (*interp.Compiler, error) { return nil, errors.New("down") }
	down := newSession(t, b, nil)
	_, _, _, err = down.Became(ctx, []string{"pipeline", "build"}, CompileOptions{})
	assert.ErrorContains(t, err, "down")
}

func TestIdOf(t *testing.T) {
	flat := map[string]any{"job": map[string]any{
		"skip": "not a map",
		"j1":   map[string]any{"name": "a"},
	}}
	id, ok := idOf(flat, "job", "a", "", "name")
	assert.Assert(t, ok)
	assert.Equal(t, id, "j1")
	_, ok = idOf(flat, "log", "a", "", "name")
	assert.Assert(t, !ok)
}

type fakeViewer struct{ err error }

func (v fakeViewer) Views(flat map[string]any, env string) ([]View, []Note, error) {
	notes := []Note{{Severity: "info", Title: "cut for " + env}}
	if v.err != nil {
		return nil, notes, v.err
	}
	return []View{
		{Destination: "z", Object: flat, Roles: map[string]string{}},
		{Destination: "a", Object: flat, Roles: map[string]string{"one": "owner"}},
	}, notes, nil
}

func TestViews(t *testing.T) {
	files := map[string]string{"/project.yaml": "id: root\n", "/pipeline/build.yaml": "id: one\nname: build\n"}
	ctx := context.Background()

	whole, err := newSession(t, fullBindings(), files).Views(ctx, CompileOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(whole.Views), 1)
	assert.Equal(t, whole.Views[0].Destination, "")
	assert.Assert(t, !whole.Views[0].Empty(), "no roles is everything, not nothing")

	b := fullBindings()
	b.Viewer = fakeViewer{}
	cut, err := newSession(t, b, files).Views(ctx, CompileOptions{Environment: "prod"})
	assert.NilError(t, err)
	assert.Equal(t, cut.Views[0].Destination, "a")
	assert.Equal(t, cut.Views[1].Destination, "z")
	assert.Assert(t, cut.Views[1].Empty())
	assert.Equal(t, cut.Notes[0].Title, "cut for prod")
	assert.Assert(t, cut.Canonical != nil)

	b.Viewer = fakeViewer{err: errors.New("refused")}
	refused, err := newSession(t, b, files).Views(ctx, CompileOptions{})
	assert.ErrorContains(t, err, "refused")
	assert.Equal(t, len(refused.Notes), 1)
	assert.Assert(t, refused.Canonical != nil)
}
