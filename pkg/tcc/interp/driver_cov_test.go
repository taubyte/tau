package interp

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func siteAttributes() []*engine.Attribute {
	return []*engine.Attribute{
		engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
		engine.Duration("timeout"),
		engine.Bytes(engine.Key("limits", "memory"), engine.Path("limits", "memory")),
		engine.String("access", engine.EnumBool("Public", []string{"public"}, []string{"public"}, [2]string{"private", "public"})),
		engine.String("type", engine.DerivedBool("Secure", map[string]bool{"https": true, "http": false}, map[bool]string{true: "https", false: "http"})),
		engine.String("protocol", engine.OnlyWhen("type", "p2p"), engine.Tag("service")),
		engine.String("gated", engine.OnlyWhen("type", "p2p")),
		engine.String("title", engine.Tag("label")),
		engine.String("file", engine.Inlined("records")),
	}
}

func driverSchema() (engine.Schema, *engine.Node) {
	site := func() *engine.Node {
		return engine.DefineGroup("site", engine.DefineIter(siteAttributes(),
			engine.Resource("site", "Site", "Site", "site"),
			engine.Nested(engine.DefineGroup("cache", engine.DefineIter([]*engine.Attribute{
				engine.Duration("ttl"),
			}))),
		))
	}
	root := engine.Root(
		[]*engine.Attribute{
			engine.String("id", engine.EmitValidation("project_id", "id-check")),
			engine.StringSlice("tags", engine.WireDrop()),
		},
		site(),
		engine.DefineGroup("space", engine.DefineIterGroup([]*engine.Attribute{
			engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
			engine.Duration("grace"),
		}, site()).With(engine.Leaf("space"), engine.Resource("space", "Space", "Space", "space"))),
		engine.DefineGroup("plain", engine.DefineIterGroup([]*engine.Attribute{
			engine.String("id"),
		}, site()).With(engine.Leaf("plain"))),
	).With(engine.Leaf("project"), engine.Identity("id", "name"))
	return engine.SchemaDefinition(root), root
}

func compileFiles(t *testing.T, files map[string]string) (map[string]any, []NextValidation, error) {
	t.Helper()
	project, root := driverSchema()
	fs := afero.NewMemMapFs()
	for path, body := range files {
		assert.NilError(t, afero.WriteFile(fs, path, []byte(body), 0o644))
	}
	c, err := New(project, root, WithVirtual(fs, "/"))
	assert.NilError(t, err)
	obj, validations, err := c.Compile(context.Background())
	if err != nil {
		return nil, validations, err
	}
	return obj.Flat(), validations, nil
}

func TestDriverProjectsEveryAnnotation(t *testing.T) {
	flat, validations, err := compileFiles(t, map[string]string{
		"/project.yaml": "id: root\ntags: [a, b]\n",
		"/site/web.yaml": "id: QmWeb\ntimeout: 2s\nlimits:\n    memory: 1KB\naccess: public\ntype: https\n" +
			"protocol: kept-only-for-p2p\ngated: dropped\ntitle: Web\ncache:\n    hot:\n        ttl: 1s\n",
		"/site/node.yaml": "id: QmNode\naccess: private\ntype: p2p\nprotocol: chat\ngated: stays\n",
		"/site/bare.yaml": "id: QmBare\ntype: ftp\n",
	})
	assert.NilError(t, err)

	assert.Equal(t, len(validations), 1)
	assert.Equal(t, validations[0].Key, "project_id")
	assert.Equal(t, validations[0].Value, "root")
	_, hasTags := flat["tags"]
	assert.Assert(t, !hasTags, "WireDrop removes the root key")

	sites := flat["site"].(map[string]any)
	web := sites["QmWeb"].(map[string]any)
	assert.Equal(t, web["name"], "web")
	assert.Equal(t, web["timeout"], int64(2_000_000_000))
	assert.DeepEqual(t, web["limits"], map[string]any{"memory": int64(1000)})
	assert.Equal(t, web["public"], true)
	_, accessKept := web["access"]
	assert.Assert(t, !accessKept, "a DropWhen value drops its wire key")
	assert.Equal(t, web["secure"], true)
	assert.Equal(t, web["type"], "https", "a derived bool keeps its source")
	_, protocol := web["protocol"]
	_, service := web["service"]
	_, gated := web["gated"]
	assert.Assert(t, !protocol && !service && !gated, "a closed gate deletes the key")
	assert.Equal(t, web["label"], "Web")
	_, title := web["title"]
	assert.Assert(t, !title)
	assert.DeepEqual(t, web["cache"], map[string]any{"hot": map[string]any{"ttl": int64(1_000_000_000)}})

	node := sites["QmNode"].(map[string]any)
	assert.Equal(t, node["public"], false)
	assert.Equal(t, node["access"], "private", "a value outside DropWhen survives")
	assert.Equal(t, node["secure"], nil)
	assert.Equal(t, node["service"], "chat", "an open gate renames to the tag")
	assert.Equal(t, node["gated"], "stays", "an open gate without a tag keeps the key")

	bare := sites["QmBare"].(map[string]any)
	assert.Equal(t, bare["public"], false, "an absent enum still projects false")
	_, secure := bare["secure"]
	assert.Assert(t, !secure, "a value with no When entry emits nothing")
}

func TestDriverPromotesContainersAndTheirResources(t *testing.T) {
	flat, validations, err := compileFiles(t, map[string]string{
		"/project.yaml":           "tags: [a]\n",
		"/space/shop/space.yaml":  "id: QmShop\n",
		"/space/shop/site/w.yaml": "id: QmW\ntimeout: 1s\n",
		"/plain/side/plain.yaml":  "id: QmSide\n",
		"/plain/side/site/p.yaml": "id: QmP\ntitle: P\n",
	})
	assert.NilError(t, err)
	assert.Equal(t, len(validations), 0, "an absent validated attribute emits nothing")

	shop := flat["space"].(map[string]any)["QmShop"].(map[string]any)
	assert.Equal(t, shop["name"], "shop")
	w := shop["site"].(map[string]any)["QmW"].(map[string]any)
	assert.Equal(t, w["name"], "w")
	assert.Equal(t, w["timeout"], int64(1_000_000_000))

	side := flat["plain"].(map[string]any)["QmSide"].(map[string]any)
	p := side["site"].(map[string]any)["QmP"].(map[string]any)
	assert.Equal(t, p["label"], "P")
}

func objOf(t *testing.T, m map[string]any) Object {
	t.Helper()
	o := object.New[object.Refrence]()
	for k, v := range m {
		if inner, ok := v.(map[string]any); ok {
			assert.NilError(t, o.Child(k).Add(objOf(t, inner)))
			continue
		}
		o.Set(k, v)
	}
	return o
}

func runDriver(t *testing.T, root *engine.Node, env Env, m map[string]any) (map[string]any, error) {
	t.Helper()
	o := objOf(t, m)
	ct := transform.NewContext[object.Refrence](context.Background())
	out, err := newCompileDriver(root, env).Process(ct, o)
	if err != nil {
		return nil, err
	}
	return out.Flat(), nil
}

func TestDriverRefusesWhatItCannotProject(t *testing.T) {
	_, root := driverSchema()
	for name, doc := range map[string]map[string]any{
		"site site: parsing timeout": {"site": map[string]any{"site": map[string]any{"id": "QmS", "timeout": "soon"}}},
		"promoting site site":        {"site": map[string]any{"site": map[string]any{"id": 5}}},
		"cache c: parsing ttl":       {"site": map[string]any{"site": map[string]any{"id": "QmS", "cache": map[string]any{"c": map[string]any{"ttl": "soon"}}}}},
		"promoting space shop":       {"space": map[string]any{"shop": map[string]any{}}},
		"site w: parsing timeout":    {"space": map[string]any{"shop": map[string]any{"id": "QmShop", "site": map[string]any{"w": map[string]any{"id": "QmW", "timeout": "soon"}}}}},
		"space shop: parsing grace":  {"space": map[string]any{"shop": map[string]any{"id": "QmShop", "grace": "x"}}},
		"promoting plain side":       {"plain": map[string]any{"side": map[string]any{}}},
	} {
		_, err := runDriver(t, root, nil, doc)
		assert.ErrorContains(t, err, name)
	}
}

func TestDriverSkipsAListAndPromotesAnEnvKeyedMap(t *testing.T) {
	root := engine.Root([]*engine.Attribute{engine.String("id")},
		engine.DefineList("steps", []*engine.Attribute{engine.String("name")}),
		engine.DefineGroup("clouds", engine.DefineIter([]*engine.Attribute{engine.String("plan")},
			PromoteEnvKeyed("clouds", "cloud", []string{"plan"}, false))),
		engine.DefineGroup("empty"),
	).With(engine.Identity("id", "name"))

	flat, err := runDriver(t, root, Env{"cloud": "eu"}, map[string]any{
		"steps":  map[string]any{"0": map[string]any{"name": "a"}},
		"clouds": map[string]any{"eu": map[string]any{"plan": "big"}, "us": map[string]any{"plan": "small"}},
	})
	assert.NilError(t, err)
	assert.Equal(t, flat["plan"], "big")
	_, clouds := flat["clouds"]
	assert.Assert(t, !clouds)
	assert.DeepEqual(t, flat["steps"], map[string]any{"0": map[string]any{"name": "a"}})
}

func TestDriverRefusesToIndexOutsideAScope(t *testing.T) {
	_, root := driverSchema()
	d := newCompileDriver(root, nil).(*CompileDriver)
	ct := transform.NewContext[object.Refrence](context.Background())
	for _, g := range []map[string]any{
		{"site": map[string]any{"w": map[string]any{"id": "QmW"}}},
		{"space": map[string]any{"shop": map[string]any{"id": "QmShop"}}},
	} {
		o := objOf(t, g)
		var group *engine.Node
		for _, child := range root.Children {
			if _, ok := g[child.Match.(string)]; ok {
				group = child
			}
		}
		assert.ErrorContains(t, d.processGroup(ct, o, group, false), "context path is empty")
	}
}

func TestDriverMixedContainerTellsDataFromResources(t *testing.T) {
	cfg := engine.DefineGroup("site", engine.DefineIter([]*engine.Attribute{engine.Duration("ttl")}))
	sites := engine.DefineGroup("site", engine.DefineIter([]*engine.Attribute{
		engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
	}, engine.Resource("site", "Site", "Site", "site")))
	space := engine.DefineIterGroup([]*engine.Attribute{engine.String("id")}, sites).
		With(engine.Resource("space", "Space", "Space", "space"), engine.Nested(cfg))
	root := engine.Root([]*engine.Attribute{engine.String("id")}, engine.DefineGroup("space", space)).
		With(engine.Identity("id", "name"))

	flat, err := runDriver(t, root, nil, map[string]any{
		"space": map[string]any{"shop": map[string]any{"id": "QmShop", "site": map[string]any{
			"own":  map[string]any{"ttl": "1s"},
			"held": map[string]any{"id": "QmHeld"},
		}}},
	})
	assert.NilError(t, err)
	held := flat["space"].(map[string]any)["QmShop"].(map[string]any)["site"].(map[string]any)
	assert.Equal(t, held["own"].(map[string]any)["ttl"], int64(1_000_000_000), "the instance's own entry gets its projections")
	assert.Equal(t, held["QmHeld"].(map[string]any)["name"], "held", "the contained resource is promoted")

	_, err = runDriver(t, root, nil, map[string]any{
		"space": map[string]any{"shop": map[string]any{"id": "QmShop", "site": map[string]any{"own": map[string]any{"ttl": "x"}}}},
	})
	assert.ErrorContains(t, err, "parsing ttl")
}

func TestHolderOfFallsBackToTheResource(t *testing.T) {
	deep := engine.Duration(engine.Key("a", "b", "c"), engine.Path("a", "b", "c"))
	o := objOf(t, map[string]any{"r": map[string]any{"a": map[string]any{"b": map[string]any{"c": "1s"}}}})
	holder, field := holderOf(o.Child("r"), deep)
	assert.Equal(t, field, "c")
	v, err := holder.Get("c")
	assert.NilError(t, err)
	assert.Equal(t, v, "1s")

	o = objOf(t, map[string]any{"r": map[string]any{"x": 1}})
	_, field = holderOf(o.Child("r"), deep)
	assert.Equal(t, field, deep.Name)
	_, field = holderOf(o.Child(0), deep)
	assert.Equal(t, field, deep.Name)
}

func TestProjectionsOfAnUnreadableInstanceDoNothing(t *testing.T) {
	sel := object.New[object.Refrence]().Child(0)
	assert.NilError(t, projectEnumBool(sel, "access", engine.EnumBoolSpec{GoName: "Public"}))
	assert.NilError(t, projectDerivedBool(sel, "type", engine.DerivedBoolSpec{GoName: "Secure"}))
	_, ok := valueOf(sel, "type")
	assert.Assert(t, !ok)
}
