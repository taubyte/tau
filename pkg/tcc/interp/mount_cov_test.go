package interp_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"gotest.tools/v3/assert"
)

func siteIter() *engine.Node {
	return engine.DefineIter([]*engine.Attribute{
		engine.String("id", engine.Required()),
		engine.String("name"),
		engine.String(engine.Key("source", "git")),
		engine.String(engine.Key("source", "branch")),
		engine.String(engine.Key("source", "path")),
		engine.String(engine.Key("source", "config")),
		engine.String(engine.Key("serve", "index"), engine.Delegable()),
		engine.String(engine.Key("serve", "mode"), engine.Delegable()),
		engine.String("title"),
	}, engine.Resource("site", "Site", "Site", "site"))
}

func mountSchema() (engine.Schema, *engine.Node) {
	root := engine.Root(
		[]*engine.Attribute{engine.String("id"), engine.String("name")},
		engine.DefineGroup("site", siteIter()),
		engine.DefineGroup("page", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required()),
			engine.String("name"),
			engine.String(engine.Key("source", "git")),
			engine.String(engine.Key("source", "config")),
		}, engine.Resource("page", "Page", "Page", "page"))),
		engine.DefineGroup("space", engine.DefineIterGroup([]*engine.Attribute{
			engine.String("id", engine.Required()),
			engine.String("name"),
		}, engine.DefineGroup("site", siteIter())).With(engine.Leaf("meta"))),
	).With(engine.Leaf("project"), engine.Identity("id", "name"))
	return engine.SchemaDefinition(root), root
}

func mountProject(t *testing.T, files map[string]string) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml", []byte("id: root\nname: shop\n"), 0o644))
	for p, b := range files {
		assert.NilError(t, afero.WriteFile(fs, p, []byte(b), 0o644))
	}
	return fs
}

func mountCompiler(t *testing.T, fs afero.Fs, opts ...interp.Option) (*interp.Compiler, error) {
	t.Helper()
	project, root := mountSchema()
	return interp.New(project, root, append([]interp.Option{interp.WithVirtual(fs, "/"),
		interp.WithLayout(interp.Layout{Resource: "{kind}/{name}.yaml", Scope: "{kind}/{name}"})}, opts...)...)
}

const (
	docsSite = "id: d1\nsource:\n    git: https://x/docs\n    branch: main\n    path: site/\n    config: tb.yaml\n"
	blogSite = "id: b1\ntitle: Blog\nsource:\n    git: https://x/blog\n    config: tb.yaml\n"
)

func memFs(t *testing.T, files map[string]string) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	for p, b := range files {
		assert.NilError(t, afero.WriteFile(fs, p, []byte(b), 0o644))
	}
	return fs
}

func discover(t *testing.T, fs afero.Fs) []interp.Mount {
	t.Helper()
	c, err := mountCompiler(t, fs)
	assert.NilError(t, err)
	mounts, err := c.Mounts(context.Background())
	assert.NilError(t, err)
	return mounts
}

func TestMountsAreWhatDelegatesAndNothingElse(t *testing.T) {
	fs := mountProject(t, map[string]string{
		"/site/docs.yaml":          docsSite,
		"/site/blog.yaml":          blogSite,
		"/site/plain.yaml":         "id: p1\nsource:\n    git: https://x/plain\n",
		"/site/bare.yaml":          "id: n1\n",
		"/space/eu/meta.yaml":      "id: s1\n",
		"/space/eu/site/shop.yaml": "id: e1\nsource:\n    git: https://x/shop\n    config: tb.yaml\n",
	})
	mounts := discover(t, fs)
	assert.Equal(t, len(mounts), 3)

	assert.Equal(t, mounts[0].At, "/.mounts/eu/site/shop")
	assert.Equal(t, mounts[0].Scope, "eu")
	assert.DeepEqual(t, mounts[0].Address, []string{"space", "eu", "site", "shop"})
	assert.Equal(t, mounts[0].String(), "eu/site/shop reads tb.yaml from https://x/shop")

	assert.Equal(t, mounts[1].At, "/.mounts/site/blog")
	assert.Equal(t, mounts[1].String(), "site/blog reads tb.yaml from https://x/blog")

	docs := mounts[2]
	assert.DeepEqual(t, docs, interp.Mount{Kind: "site", Name: "docs", ID: "d1",
		Repo: "https://x/docs", Branch: "main", Path: "site/", Config: "tb.yaml", At: "/.mounts/site/docs",
		Address: []string{"site", "docs"}})
	assert.Equal(t, docs.String(), "site/docs reads tb.yaml from https://x/docs@main")

	assert.Equal(t, interp.MountPoint("a/b", "site", "x"), "/.mounts/a/b/site/x")
	assert.Equal(t, interp.MountPoint("", "site", "x"), "/.mounts/site/x")
}

func TestMountsOfAProjectThatDoesNotCompile(t *testing.T) {
	c, err := mountCompiler(t, mountProject(t, map[string]string{"/site/docs.yaml": "title: no id\n"}))
	assert.NilError(t, err)
	_, err = c.Mounts(context.Background())
	assert.ErrorContains(t, err, "id")
}

func TestWithMountedRefusesAnIncompleteAnswer(t *testing.T) {
	fs := mountProject(t, map[string]string{"/site/docs.yaml": docsSite})
	m := discover(t, fs)[0]

	_, err := mountCompiler(t, fs, interp.WithMounted(interp.Mount{}, afero.NewMemMapFs(), "c"))
	assert.ErrorContains(t, err, "no mount point")
	_, err = mountCompiler(t, fs, interp.WithMounted(interp.Mount{At: m.At}, afero.NewMemMapFs(), "c"))
	assert.ErrorContains(t, err, "no document address")
	_, err = mountCompiler(t, fs, interp.WithMounted(m, nil, "c"))
	assert.ErrorContains(t, err, "with no filesystem")
	_, err = mountCompiler(t, fs, interp.WithMounted(m, afero.NewMemMapFs(), ""))
	assert.ErrorContains(t, err, "provenance is not optional")
}

func TestAFillIsCompiledAsIfAuthoredAndRecorded(t *testing.T) {
	fs := mountProject(t, map[string]string{"/site/docs.yaml": docsSite, "/site/blog.yaml": blogSite})
	var opts []interp.Option
	for _, m := range discover(t, fs) {
		fill := memFs(t, map[string]string{
			"site/tb.yaml": "serve:\n    index: home.html\n    mode: spa\n",
			"tb.yaml":      "serve:\n    index: blog.html\n",
		})
		opts = append(opts, interp.WithMounted(m, fill, "commit-"+m.Name))
	}
	c, err := mountCompiler(t, fs, opts...)
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	flat := obj.Flat()

	sites := flat["site"].(map[string]any)
	docs := sites["d1"].(map[string]any)["serve"].(map[string]any)
	assert.DeepEqual(t, docs, map[string]any{"index": "home.html", "mode": "spa"})
	blog := sites["b1"].(map[string]any)
	assert.Equal(t, blog["serve"].(map[string]any)["index"], "blog.html")
	assert.Equal(t, blog["title"], "Blog")

	rec := interp.MountedOf(flat)
	assert.DeepEqual(t, rec, map[string]interp.Fill{
		"b1": {Repo: "https://x/blog", Commit: "commit-blog", Config: "tb.yaml", Kind: "site", Name: "blog"},
		"d1": {Repo: "https://x/docs", Branch: "main", Commit: "commit-docs", Config: "tb.yaml", Kind: "site", Name: "docs"},
	})

	unmounted, err := mountCompiler(t, fs)
	assert.NilError(t, err)
	plain, _, err := unmounted.Compile(context.Background())
	assert.NilError(t, err)
	assert.Equal(t, len(interp.MountedOf(plain.Flat())), 0, "a compile that mounted nothing records nothing")
	_, has := plain.Flat()[interp.MountedKey]
	assert.Assert(t, !has)
}

func TestAFillThatSaysWhatItMayNotIsRefused(t *testing.T) {
	type testCase struct {
		files map[string]string
		fill  map[string]string
		want  string
	}
	for name, tc := range map[string]testCase{
		"undelegated key": {
			map[string]string{"/site/blog.yaml": blogSite},
			map[string]string{"tb.yaml": "title: mine\n"},
			`sets "title", which is not delegated — site may carry serve`,
		},
		"kind that delegates nothing": {
			map[string]string{"/page/about.yaml": "id: a1\nsource:\n    git: https://x/about\n    config: tb.yaml\n"},
			map[string]string{"tb.yaml": "serve:\n    index: x\n"},
			"page may carry nothing",
		},
		"two authors": {
			map[string]string{"/site/blog.yaml": blogSite + "serve:\n    mode: spa\n"},
			map[string]string{"tb.yaml": "serve:\n    index: x\n"},
			`"serve" is set both here and in tb.yaml of https://x/blog`,
		},
		"missing file": {
			map[string]string{"/site/docs.yaml": docsSite},
			map[string]string{"tb.yaml": "serve: {}\n"},
			"names site/tb.yaml and it could not be read",
		},
		"not yaml": {
			map[string]string{"/site/blog.yaml": blogSite},
			map[string]string{"tb.yaml": "- a\n- b\n"},
			"tb.yaml is not readable as yaml",
		},
	} {
		t.Run(name, func(t *testing.T) {
			fs := mountProject(t, tc.files)
			m := discover(t, fs)[0]
			_, err := mountCompiler(t, fs, interp.WithMounted(m, memFs(t, tc.fill), "c1"))
			assert.Assert(t, errors.Is(err, interp.ErrFill), err)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

type mounter struct {
	asked []string
	fill  string
	err   error
	nofs  bool
}

func (f *mounter) Mount(_ context.Context, m interp.Mount) (afero.Fs, string, error) {
	f.asked = append(f.asked, m.At)
	if f.err != nil {
		return nil, "", f.err
	}
	if f.nofs {
		return nil, "c", nil
	}
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "tb.yaml", []byte(f.fill), 0o644); err != nil {
		return nil, "", err
	}
	if err := afero.WriteFile(fs, "site/tb.yaml", []byte(f.fill), 0o644); err != nil {
		return nil, "", err
	}
	return fs, "from-mounter", nil
}

func TestAMounterAnswersDuringTheCompile(t *testing.T) {
	_, err := mountCompiler(t, mountProject(t, nil), interp.WithMounter(nil))
	assert.ErrorContains(t, err, "a nil mounter")

	fs := mountProject(t, map[string]string{"/site/docs.yaml": docsSite, "/site/blog.yaml": blogSite})
	docs := discover(t, fs)[1]
	hand := memFs(t, map[string]string{"site/tb.yaml": "serve:\n    index: by-hand.html\n"})

	f := &mounter{fill: "serve:\n    index: fetched.html\n"}
	c, err := mountCompiler(t, fs, interp.WithMounter(f), interp.WithMounted(docs, hand, "hand"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	assert.DeepEqual(t, f.asked, []string{"/.mounts/site/blog"})

	sites := obj.Flat()["site"].(map[string]any)
	assert.Equal(t, sites["d1"].(map[string]any)["serve"].(map[string]any)["index"], "by-hand.html")
	assert.Equal(t, sites["b1"].(map[string]any)["serve"].(map[string]any)["index"], "fetched.html")
	rec := interp.MountedOf(obj.Flat())
	assert.Equal(t, rec["d1"].Commit, "hand")
	assert.Equal(t, rec["b1"].Commit, "from-mounter")

	again, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	assert.Equal(t, len(f.asked), 1, "asked once per compiler")
	assert.Equal(t, interp.MountedOf(again.Flat())["b1"].Commit, "from-mounter")
}

func TestAMounterWithNothingToAnswer(t *testing.T) {
	f := &mounter{}
	c, err := mountCompiler(t, mountProject(t, map[string]string{"/site/bare.yaml": "id: n1\n"}), interp.WithMounter(f))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	assert.Equal(t, len(f.asked), 0)
	assert.Equal(t, len(interp.MountedOf(obj.Flat())), 0)
}

func TestAMounterThatFailsFailsTheCompile(t *testing.T) {
	broken := mountProject(t, map[string]string{"/site/docs.yaml": "title: no id\n"})
	c, err := mountCompiler(t, broken, interp.WithMounter(&mounter{}))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "id")

	type testCase struct {
		f    *mounter
		want string
	}
	for name, tc := range map[string]testCase{
		"fetch error": {&mounter{err: fmt.Errorf("no route")}, "mounting site/blog reads tb.yaml from https://x/blog: no route"},
		"no fs":       {&mounter{nofs: true}, "was answered with no filesystem"},
		"bad fill":    {&mounter{fill: "title: mine\n"}, `sets "title", which is not delegated`},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := mountCompiler(t, mountProject(t, map[string]string{"/site/blog.yaml": blogSite}), interp.WithMounter(tc.f))
			assert.NilError(t, err)
			_, _, err = c.Compile(context.Background())
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestAFillInsideAScopeLandsOnItsResource(t *testing.T) {
	fs := mountProject(t, map[string]string{
		"/space/eu/meta.yaml":      "id: s1\nname: eu\n",
		"/space/eu/site/shop.yaml": "id: e1\nsource:\n    git: https://x/shop\n    config: tb.yaml\n",
	})
	var opts []interp.Option
	for _, m := range discover(t, fs) {
		opts = append(opts, interp.WithMounted(m, memFs(t, map[string]string{"tb.yaml": "serve:\n    index: shop.html\n"}), "commit-shop"))
	}
	c, err := mountCompiler(t, fs, opts...)
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	flat := obj.Flat()

	shop := flat["space"].(map[string]any)["s1"].(map[string]any)["site"].(map[string]any)["e1"].(map[string]any)
	assert.Equal(t, shop["serve"].(map[string]any)["index"], "shop.html")
	assert.DeepEqual(t, interp.MountedOf(flat), map[string]interp.Fill{
		"e1": {Repo: "https://x/shop", Commit: "commit-shop", Config: "tb.yaml", Kind: "site", Name: "shop", Scope: "eu"},
	})
}
