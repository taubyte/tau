package interp_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/interp/decompile"
	"github.com/taubyte/tau/pkg/tcc/object"
	"gotest.tools/v3/assert"
)

func dcLib() *engine.Node {
	return engine.DefineIter([]*engine.Attribute{
		engine.String("id", engine.Required()), engine.String("name"),
	}, engine.Resource("lib", "Lib", "Lib", "lib"))
}

func dcDomain() *engine.Node {
	return engine.DefineIter([]*engine.Attribute{
		engine.String("id", engine.Required()), engine.String("name"), engine.String("fqdn"),
		engine.String("zone", engine.Inlined("records")),
	}, engine.Resource("domain", "Domain", "Domain", "domain"))
}

func dcFn() *engine.Node {
	return engine.DefineIter([]*engine.Attribute{
		engine.String("id", engine.Required(), engine.Validator(fnID)),
		engine.String("name"),
		engine.String("type", engine.DerivedBool("Secure", map[string]bool{"https": true, "http": false}, map[bool]string{true: "https", false: "http"})),
		engine.String("protocol", engine.OnlyWhen("type", "p2p"), engine.Tag("service")),
		engine.String("net", engine.EnumBool("Local", []string{"host"}, []string{"host", "all"}, [2]string{"host", "all"})),
		engine.Duration("timeout", engine.Tag("wait")),
		engine.StringSlice("domains", engine.Ref("domain")),
		engine.String("source", engine.Ref("lib", engine.Prefix("libraries/"), engine.FlatValue())),
		engine.String("uses", engine.Ref("lib")),
	},
		engine.Nested(
			engine.DefineGroup("ports", engine.DefineIter([]*engine.Attribute{
				engine.String("proto", engine.Tag("p")),
				engine.Duration("idle"),
				engine.String("lib", engine.Ref("lib")),
			})),
			engine.DefineList("steps", []*engine.Attribute{engine.String("run")}),
		),
		engine.Resource("fn", "Fn", "Fn", "fn"))
}

func fnID(s string) error {
	if !strings.HasPrefix(s, "F") {
		return fmt.Errorf("%q is not a function id", s)
	}
	return nil
}

func dcAgent() *engine.Node {
	return engine.DefineIterGroup([]*engine.Attribute{
		engine.String("id", engine.Required()), engine.String("name"), engine.String("uses", engine.Ref("lib")),
	}, engine.DefineGroup("fn", dcFn())).With(
		engine.Resource("agent", "Agent", "Agent", "agent"),
		engine.Nested(engine.DefineGroup("fn", engine.DefineIter([]*engine.Attribute{
			engine.String("lib", engine.Ref("lib")), engine.Duration("timeout"),
		}))),
		engine.Leaf("meta"))
}

func dcSchema() (engine.Schema, *engine.Node) {
	root := engine.Root(
		[]*engine.Attribute{engine.String("id"), engine.String("name")},
		engine.DefineGroup("agent", dcAgent()),
		engine.DefineGroup("lib", dcLib()),
		engine.DefineGroup("domain", dcDomain()),
		engine.DefineGroup("fn", dcFn()),
		engine.DefineGroup("app", engine.DefineIterGroup([]*engine.Attribute{
			engine.String("id", engine.Required()), engine.String("name"),
		}, engine.DefineGroup("lib", dcLib()), engine.DefineGroup("fn", dcFn())).With(engine.Leaf("meta"))),
	).With(engine.Leaf("project"), engine.Identity("id", "name"))
	return engine.SchemaDefinition(root), root
}

var dcFiles = map[string]string{
	"/project.yaml":     "id: root\nname: shop\n",
	"/lib/core.yaml":    "id: L1\n",
	"/domain/main.yaml": "id: D1\nfqdn: a.example.com\nzone: zones/a.zone\n",
	"/zones/a.zone":     "@ IN A 1.2.3.4\n",
	"/fn/api.yaml": "id: F1\ntype: https\nnet: host\ntimeout: 20s\ndomains: [main]\nsource: libraries/core\nuses: core\n" +
		"ports:\n    web:\n        proto: tcp\n        idle: 5m\n        lib: core\nsteps:\n    - run: a\n    - run: b\n",
	"/fn/peer.yaml":           "id: F2\ntype: p2p\nprotocol: chat\nnet: subnet\nsource: .\n",
	"/app/eu/meta.yaml":       "id: A1\n",
	"/app/eu/lib/own.yaml":    "id: L2\n",
	"/agent/bot/meta.yaml":    "id: G1\nuses: core\nfn:\n    checkout:\n        lib: core\n        timeout: 1m\n",
	"/agent/bot/fn/tool.yaml": "id: F4\ntype: http\nsource: .\nuses: core\ntimeout: 3s\n",
	"/app/eu/fn/job.yaml":     "id: F3\ntype: http\nsource: libraries/own\nuses: core\n",
}

func dcCompile(t *testing.T, fs afero.Fs) map[string]any {
	t.Helper()
	project, root := dcSchema()
	c, err := interp.New(project, root, interp.WithVirtual(fs, "/"),
		interp.WithLayout(interp.Layout{Resource: "{kind}/{name}.yaml", Scope: "{kind}/{name}"}))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	return obj.Flat()
}

func dcDecompile(t *testing.T, flat map[string]any) afero.Fs {
	t.Helper()
	project, root := dcSchema()
	src := memFs(t, dcFiles)
	c, err := interp.New(project, root, interp.WithVirtual(src, "/"),
		interp.WithLayout(interp.Layout{Resource: "{kind}/{name}.yaml", Scope: "{kind}/{name}"}))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	assert.DeepEqual(t, obj.Flat(), flat)

	out := afero.NewMemMapFs()
	d, err := decompile.New(project, root, decompile.WithVirtual(out, "/"))
	assert.NilError(t, err)
	assert.NilError(t, d.Decompile(obj))
	return out
}

func readDoc(t *testing.T, fs afero.Fs, path string) map[string]any {
	t.Helper()
	body, err := afero.ReadFile(fs, path)
	assert.NilError(t, err, path)
	var doc map[string]any
	assert.NilError(t, yaml.Unmarshal(body, &doc))
	return doc
}

func TestDecompileWritesWhatWasAuthored(t *testing.T) {
	flat := dcCompile(t, memFs(t, dcFiles))
	out := dcDecompile(t, flat)

	api := readDoc(t, out, "/fn/api.yaml")
	assert.Equal(t, api["id"], "F1")
	assert.Equal(t, api["type"], "https")
	assert.Equal(t, api["net"], "host")
	assert.Equal(t, api["timeout"], "20s")
	assert.DeepEqual(t, api["domains"], []any{"main"})
	assert.Equal(t, api["source"], "libraries/core")
	assert.Equal(t, api["uses"], "core")
	assert.DeepEqual(t, api["ports"], map[string]any{"web": map[string]any{"proto": "tcp", "idle": "5m0s", "lib": "core"}})
	assert.DeepEqual(t, api["steps"], []any{map[string]any{"run": "a"}, map[string]any{"run": "b"}})
	for _, synthesized := range []string{"secure", "local", "wait", "name"} {
		_, has := api[synthesized]
		assert.Assert(t, !has, "%s is compiled, not authored", synthesized)
	}

	peer := readDoc(t, out, "/fn/peer.yaml")
	assert.Equal(t, peer["protocol"], "chat", "renamed back only when type is p2p")
	assert.Equal(t, peer["net"], "subnet", "a preserved source value wins over the bool")
	assert.Equal(t, peer["source"], ".", "a value outside the prefix is not a reference")

	job := readDoc(t, out, "/app/eu/fn/job.yaml")
	assert.Equal(t, job["source"], "libraries/own", "the scope's own library first")
	assert.Equal(t, job["uses"], "core", "then the project's")
	assert.Equal(t, job["net"], "all", "rebuilt from the bool when the source key was dropped")
	assert.Equal(t, readDoc(t, out, "/app/eu/meta.yaml")["id"], "A1")
	main := readDoc(t, out, "/domain/main.yaml")
	assert.DeepEqual(t, main, map[string]any{"id": "D1", "fqdn": "a.example.com", "zone": "zones/a.zone"})
	zone, err := afero.ReadFile(out, "/zones/a.zone")
	assert.NilError(t, err)
	assert.Equal(t, string(zone), "@ IN A 1.2.3.4\n")

	bot := readDoc(t, out, "/agent/bot/meta.yaml")
	assert.Equal(t, bot["uses"], "core")
	assert.DeepEqual(t, bot["fn"], map[string]any{"checkout": map[string]any{"lib": "core", "timeout": "1m0s"}})
	tool := readDoc(t, out, "/agent/bot/fn/tool.yaml")
	assert.Equal(t, tool["uses"], "core")
	assert.Equal(t, tool["timeout"], "3s")

	assert.DeepEqual(t, dcCompile(t, out), flat)
}

func TestDecompileRefusesAnObjectItCannotInvert(t *testing.T) {
	project, root := dcSchema()
	for name, tc := range map[string]struct {
		mutate func(fn object.Object[object.Refrence])
		want   string
	}{
		"unknown listed id":     {func(fn object.Object[object.Refrence]) { fn.Set("domains", []string{"D9"}) }, "domain ID D9 not found"},
		"list that is not":      {func(fn object.Object[object.Refrence]) { fn.Set("domains", "D1") }, "is not a []string"},
		"unknown prefixed id":   {func(fn object.Object[object.Refrence]) { fn.Set("source", "libraries/L9") }, "lib ID L9 not found"},
		"prefixed not a string": {func(fn object.Object[object.Refrence]) { fn.Set("source", 7) }, "is not a string"},
		"unknown id":            {func(fn object.Object[object.Refrence]) { fn.Set("uses", "L9") }, "lib ID L9 not found"},
		"bad scalar":            {func(fn object.Object[object.Refrence]) { fn.Set("wait", "soon") }, "fn F1"},
		"bad nested scalar": {func(fn object.Object[object.Refrence]) {
			ports, err := fn.Child("ports").Object()
			assert.NilError(t, err)
			ports.Child("web").Set("idle", "soon")
		}, "ports/web"},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := interp.New(project, root, interp.WithVirtual(memFs(t, dcFiles), "/"),
				interp.WithLayout(interp.Layout{Resource: "{kind}/{name}.yaml", Scope: "{kind}/{name}"}))
			assert.NilError(t, err)
			obj, _, err := c.Compile(context.Background())
			assert.NilError(t, err)
			tc.mutate(fnF1(t, obj))

			d, err := decompile.New(project, root, decompile.WithVirtual(afero.NewMemMapFs(), "/"))
			assert.NilError(t, err)
			assert.ErrorContains(t, d.Decompile(obj), tc.want)
		})
	}
}

func TestDecompileWithNoBoolToRestoreFrom(t *testing.T) {
	project, root := dcSchema()
	c, err := interp.New(project, root, interp.WithVirtual(memFs(t, dcFiles), "/"),
		interp.WithLayout(interp.Layout{Resource: "{kind}/{name}.yaml", Scope: "{kind}/{name}"}))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	fnF1(t, obj).Delete("local")

	out := afero.NewMemMapFs()
	d, err := decompile.New(project, root, decompile.WithVirtual(out, "/"))
	assert.NilError(t, err)
	assert.NilError(t, d.Decompile(obj))
	_, has := readDoc(t, out, "/fn/api.yaml")["net"]
	assert.Assert(t, !has, "nothing to restore the enum from, so it is not invented")
}

func fnF1(t *testing.T, obj interp.Object) object.Object[object.Refrence] {
	t.Helper()
	fns, err := obj.Child("fn").Object()
	assert.NilError(t, err)
	fn, err := fns.Child("F1").Object()
	assert.NilError(t, err)
	return fn
}
