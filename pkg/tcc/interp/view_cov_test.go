package interp

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

func flowJobs() *engine.Node {
	return engine.DefineGroup("job", engine.DefineIter([]*engine.Attribute{
		engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
		engine.StringSlice("runs"),
		engine.String("owner"),
		engine.String("file", engine.Inlined("records")),
	},
		engine.Resource("job", "Job", "Job", "job"),
		engine.Nested(engine.DefineGroup("hooks", engine.DefineIter([]*engine.Attribute{
			engine.StringSlice("on"),
		}))),
	))
}

func flowSchema() (engine.Schema, *engine.Node) {
	root := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		flowJobs(),
		engine.DefineGroup("pipeline", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required()),
			engine.String("name"),
		},
			engine.Nested(engine.DefineList("stages", []*engine.Attribute{
				engine.String("name", engine.Required()),
				engine.String("run"),
			})),
			engine.Resource("pipeline", "Pipeline", "Pipeline", "pipeline"),
			Lowers(Into("job",
				PerEach("stages"),
				Named(Concat(Attr("name"), Text("-"), Each("name"))),
				Carries(Concat(Text("stage "), Each("name"))),
				Writes(Concat(Attr("name"), Text("-"), Each("name")), On("job", Each("run")), At(Text("runs"))),
				Writes(Attr("name"), On("job", Each("run")), At(Text("owner"))),
				Writes(Each("name"), On("job", Each("run")), At(Text("hooks"), Text("main"), Text("on"))),
			)),
		)),
	).With(engine.Leaf("project"), engine.Identity("id", "name"))
	return engine.SchemaDefinition(root), root
}

func flowCompiler(t *testing.T, files map[string]string, opts ...Option) *Compiler {
	t.Helper()
	project, root := flowSchema()
	fs := afero.NewMemMapFs()
	for path, body := range files {
		assert.NilError(t, afero.WriteFile(fs, path, []byte(body), 0o644))
	}
	c, err := New(project, root, append([]Option{WithVirtual(fs, "/"),
		WithLayout(Layout{Resource: "{kind}/{name}.yaml", Scope: "{kind}/{name}"})}, opts...)...)
	assert.NilError(t, err)
	return c
}

func jobsByName(t *testing.T, obj Object) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	jobs, _ := obj.Flat()[ProjectKey].(map[string]any)["job"].(map[string]any)
	for _, v := range jobs {
		j := v.(map[string]any)
		out[j["name"].(string)] = j
	}
	return out
}

const flowPipeline = "id: one\nstages:\n    - name: compile\n      run: cc\n    - name: test\n      run: ct\n"

func TestLoweringCompilesWhatItProducesAsConfig(t *testing.T) {
	c := flowCompiler(t, map[string]string{
		"/project.yaml":        "id: root\n",
		"/pipeline/build.yaml": flowPipeline,
		"/job/cc.yaml":         "id: QmCC\nfile: cc.zone\n",
		"/job/ct.yaml":         "id: QmCT\nowner: me\nruns: [build-test]\nhooks:\n    main:\n        on: [test]\n",
		"/cc.zone":             "x\n",
	})
	assert.Assert(t, c.LoweredFS() == nil, "nothing is lowered before a compile")
	assert.Equal(t, c.Root(), c.compileRoot)

	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	jobs := jobsByName(t, obj)

	assert.Assert(t, jobs["build-compile"] != nil)
	assert.Assert(t, jobs["build-test"] != nil)
	assert.DeepEqual(t, jobs["cc"]["runs"], []string{"build-compile"})
	assert.Equal(t, jobs["cc"]["owner"], "build", "an empty scalar takes the value")
	assert.DeepEqual(t, jobs["cc"]["hooks"], map[string]any{"main": map[string]any{"on": []string{"compile"}}})
	assert.DeepEqual(t, jobs["cc"]["records"], []any{"x"})
	assert.Equal(t, jobs["ct"]["owner"], "me", "an author's own value is never replaced")
	assert.DeepEqual(t, jobs["ct"]["runs"], []string{"build-test"})
	assert.DeepEqual(t, jobs["ct"]["hooks"], map[string]any{"main": map[string]any{"on": []string{"test"}}})

	lowered := LoweredOf(obj.Flat())
	var values []string
	for _, g := range lowered.Entries {
		values = append(values, g.Value)
	}
	sort.Strings(values)
	assert.DeepEqual(t, values, []string{"build", "build-compile", "compile"})

	exists, err := afero.Exists(c.LoweredFS(), "/job/build-compile.yaml")
	assert.NilError(t, err)
	assert.Assert(t, exists, "the view holds what the second pass read back")

	validations, err := c.Validate(context.Background())
	assert.NilError(t, err)
	assert.Equal(t, len(validations), 0)
}

func TestLoweringWithNothingToLowerIsTheAuthoredObject(t *testing.T) {
	c := flowCompiler(t, map[string]string{"/project.yaml": "id: root\n", "/job/cc.yaml": "id: QmCC\n"})
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, c.LoweredFS() == nil)
	assert.Equal(t, len(jobsByName(t, obj)), 1)
	assert.Equal(t, len(LoweredOf(obj.Flat()).Resources), 0)
}

func TestLoweringRefuses(t *testing.T) {
	for want, files := range map[string]map[string]string{
		"job/build-compile is written here AND is what pipeline/build": {
			"/pipeline/build.yaml":    flowPipeline,
			"/job/build-compile.yaml": "id: QmTaken\n",
		},
		"job/build-x is produced twice": {
			"/pipeline/build.yaml": "id: one\nstages:\n    - name: x\n    - name: x\n",
		},
	} {
		files["/project.yaml"] = "id: root\n"
		_, _, err := flowCompiler(t, files).Compile(context.Background())
		assert.ErrorContains(t, err, want)
	}
}

func TestLoweringNeedsALayout(t *testing.T) {
	project, root := flowSchema()
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml", []byte("id: root\n"), 0o644))
	c, err := New(project, root, WithVirtual(fs, "/"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "declares lowerings but no layout")
}

func TestListAtFollowsTheSchema(t *testing.T) {
	_, root := flowSchema()
	assert.Assert(t, listAt(root, "job", []string{"runs"}))
	assert.Assert(t, !listAt(root, "job", []string{"owner"}))
	assert.Assert(t, listAt(root, "job", []string{"hooks", "main", "on"}))
	assert.Assert(t, !listAt(root, "job", []string{"nothing", "main", "on"}))
	assert.Assert(t, !listAt(root, "job", []string{"hooks", "main"}))
	assert.Assert(t, !listAt(root, "nokind", []string{"runs"}))
	runs, _ := childGroup(iterOfKind(root, "job"), "runs")
	assert.Assert(t, runs == nil)
}

func TestGrantNeverReplacesAnAuthorsValue(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/job/cc.yaml", []byte("owner: me\nruns: [a]\n"), 0o644))
	seer, err := yaseer.New(yaseer.VirtualFS(fs, "/"))
	assert.NilError(t, err)
	on := []string{"job", "cc"}

	changed, err := grant(seer, granted{at: on, path: []string{"owner"}, value: "you"}, false)
	assert.NilError(t, err)
	assert.Assert(t, !changed)
	changed, err = grant(seer, granted{at: on, path: []string{"runs"}, value: "a"}, true)
	assert.NilError(t, err)
	assert.Assert(t, !changed)
	changed, err = grant(seer, granted{at: on, path: []string{"runs"}, value: "b"}, true)
	assert.NilError(t, err)
	assert.Assert(t, changed)
	changed, err = grant(seer, granted{at: on, path: []string{"tags"}, value: "t"}, true)
	assert.NilError(t, err)
	assert.Assert(t, changed, "an absent list is the first value at that path")
	assert.NilError(t, seer.Sync())

	var runs, tags []string
	assert.NilError(t, at(seer, on).Get("runs").Value(&runs))
	assert.NilError(t, at(seer, on).Get("tags").Value(&tags))
	assert.DeepEqual(t, runs, []string{"a", "b"})
	assert.DeepEqual(t, tags, []string{"t"})
}

func TestSourceLabelDropsTheElement(t *testing.T) {
	assert.Equal(t, sourceLabel(placement{src: "workflow/checkout (step 2)"}), "workflow/checkout")
	assert.Equal(t, sourceLabel(placement{src: "workflow/checkout"}), "workflow/checkout")
}

func TestLowerReadsUnderTheCompilersOwnBinding(t *testing.T) {
	c := flowCompiler(t, map[string]string{
		"/project.yaml":        "id: root\n",
		"/pipeline/build.yaml": flowPipeline,
	})
	authored, _, err := c.Authored(context.Background())
	assert.NilError(t, err)
	obj, _, err := c.lower(context.Background(), authored, nil)
	assert.NilError(t, err)
	jobs := jobsByName(t, obj)
	assert.Assert(t, jobs["build-compile"] != nil && jobs["build-test"] != nil)
}

type unreadable struct {
	afero.Fs
	path string
}

func (u unreadable) Open(name string) (afero.File, error) {
	if name == u.path {
		return nil, afero.ErrFileNotFound
	}
	return u.Fs.Open(name)
}

func TestOverlayRefusesWhatItCannotCopy(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml", []byte("id: root\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/pipeline/build.yaml", []byte(flowPipeline), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/notes.txt", []byte("x"), 0o644))
	broken := unreadable{Fs: fs, path: "/notes.txt"}

	_, err := overlay(broken)
	assert.Assert(t, err != nil)

	project, root := flowSchema()
	c, err := New(project, root, WithVirtual(broken, "/"), WithLayout(Layout{Resource: "{kind}/{name}.yaml"}))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.Assert(t, err != nil, "a view that cannot be built is not a compile")
}

func scopeSchema() (engine.Schema, *engine.Node) {
	id := func() *engine.Attribute {
		return engine.String("id", engine.Required(), engine.Generated(engine.GenCID))
	}
	jobs := func(attrs ...*engine.Attribute) *engine.Node {
		return engine.DefineGroup("job", engine.DefineIter(append([]*engine.Attribute{id()}, attrs...),
			engine.Resource("job", "Job", "Job", "job")))
	}
	after := engine.DefineIterGroup([]*engine.Attribute{id(), engine.String("name")}, jobs()).With(engine.Leaf("scope"))
	root := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		jobs(engine.String("feeds")),
		engine.DefineGroup("space", engine.DefineIterGroup([]*engine.Attribute{id()}, jobs()).With(engine.Leaf("space"))),
		engine.DefineGroup("pipeline", Lowerable(engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required()),
			engine.String("name"),
		},
			engine.Resource("pipeline", "Pipeline", "Pipeline", "pipeline"),
			Lowers(Into("job", Named(Concat(Attr("name"), Text("-run"))))),
		), after)),
	).With(engine.Leaf("project"), engine.Identity("id", "name"))
	return engine.SchemaDefinition(root), root
}

func scopeCompile(t *testing.T, files map[string]string) (Object, error) {
	t.Helper()
	project, root := scopeSchema()
	fs := afero.NewMemMapFs()
	files["/project.yaml"] = "id: root\n"
	for path, body := range files {
		assert.NilError(t, afero.WriteFile(fs, path, []byte(body), 0o644))
	}
	c, err := New(project, root, WithVirtual(fs, "/"),
		WithLayout(Layout{Resource: "{kind}/{name}.yaml", Scope: "{kind}/{name}"}))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	return obj, err
}

func TestALoweredScopeHoldsWhatItProduces(t *testing.T) {
	obj, err := scopeCompile(t, map[string]string{
		"/pipeline/build.yaml":        "id: one\n",
		"/space/shop/space.yaml":      "id: QmShop\n",
		"/space/shop/job/inside.yaml": "id: QmInside\n",
	})
	assert.NilError(t, err)
	pipelines := obj.Flat()[ProjectKey].(map[string]any)["pipeline"].(map[string]any)
	assert.Equal(t, len(pipelines), 1)
	for _, p := range pipelines {
		var names []string
		for _, j := range p.(map[string]any)["job"].(map[string]any) {
			names = append(names, j.(map[string]any)["name"].(string))
		}
		assert.DeepEqual(t, names, []string{"build-run"})
	}
}

func TestALoweredScopeMayNotTakeAnAuthoredScopesName(t *testing.T) {
	_, err := scopeCompile(t, map[string]string{
		"/pipeline/build.yaml":    "id: one\n",
		"/space/build/space.yaml": "id: QmBuild\n",
	})
	assert.ErrorContains(t, err, "is the scope pipeline/build becomes")
}

func chainRoot(into map[string][]string, kinds ...string) *engine.Node {
	var groups []*engine.Node
	for _, k := range kinds {
		opts := []any{engine.Resource(k, k, k, k)}
		var ls []Lowering
		for _, to := range into[k] {
			ls = append(ls, Into(to, Named(Concat(Attr("name"), Text("-"+to)))))
		}
		if len(ls) > 0 {
			opts = append(opts, Lowers(ls...))
		}
		groups = append(groups, engine.DefineGroup(k, engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
			engine.String("name"),
		}, opts...)))
	}
	return engine.Root([]*engine.Attribute{engine.String("id")}, groups...).
		With(engine.Leaf("project"), engine.Identity("id", "name"))
}

func chainCompiler(root *engine.Node, first string) (*Compiler, error) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/project.yaml", []byte("id: root\n"), 0o644); err != nil {
		return nil, err
	}
	if err := afero.WriteFile(fs, "/"+first+"/p.yaml", []byte("id: QmP\n"), 0o644); err != nil {
		return nil, err
	}
	return New(engine.SchemaDefinition(root), root, WithVirtual(fs, "/"),
		WithLayout(Layout{Resource: "{kind}/{name}.yaml", Scope: "{kind}/{name}"}))
}

func TestALoweringCycleIsRefusedAtNew(t *testing.T) {
	_, err := chainCompiler(chainRoot(map[string][]string{"pipeline": {"pipeline"}}, "pipeline"), "pipeline")
	assert.ErrorContains(t, err, "lowerings cycle: pipeline -> pipeline")

	_, err = chainCompiler(chainRoot(map[string][]string{"a": {"b"}, "b": {"a"}}, "a", "b"), "a")
	assert.ErrorContains(t, err, "a -> b -> a")
}

func TestADeepAcyclicChainLowersAllTheWay(t *testing.T) {
	into := map[string][]string{}
	var kinds []string
	for i := 1; i <= 10; i++ {
		kinds = append(kinds, fmt.Sprintf("k%d", i))
		if i < 10 {
			into[kinds[i-1]] = []string{fmt.Sprintf("k%d", i+1)}
		}
	}
	c, err := chainCompiler(chainRoot(into, kinds...), "k1")
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	name := "p"
	for _, k := range kinds[1:] {
		name += "-" + k
		group, _ := obj.Flat()[ProjectKey].(map[string]any)[k].(map[string]any)
		assert.Assert(t, group != nil, "%s missing from %v", k, obj)
		found := false
		for _, r := range group {
			if m, _ := r.(map[string]any); m != nil && m["name"] == name {
				found = true
			}
		}
		assert.Assert(t, found, "%s has no %s: %v", k, name, group)
	}
}
