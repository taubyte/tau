package hostmodule_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke/hostmodule"
	witfs "github.com/taubyte/tau/pkg/invoke/wasm/wit"
)

// everyType holds every type a module may take or return.
const everyType = `package acme:shapes@1.0.0;

interface all {
    record point { x: s32, y: s32 }
    variant shape { dot, circle(u32), named(string) }
    enum colour { red, green, blue }
    flags access { read, write, exec }

    echo: func(p: point, s: shape, c: colour, a: access, o: option<string>, t: tuple<u8, string>, b: list<u8>, n: list<string>) -> result<list<point>, shape>;
    nothing: func();
}
`

func source(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, body := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return fsys
}

func TestAnInterfaceIsReadOnceFromItsWIT(t *testing.T) {
	d, err := hostmodule.Define(source(map[string]string{"shapes.wit": everyType}), ".", "acme:shapes/all@1.0.0")
	require.NoError(t, err)
	require.Equal(t, "acme:shapes/all@1.0.0", d.Name())
	require.Equal(t, []string{"echo", "nothing"}, d.Funcs())
	require.NotNil(t, d.Signature("echo"))
	require.Len(t, d.Signature("echo").Desc.Params, 8)

	fsys, dir := d.Source()
	require.NotNil(t, fsys)
	require.Equal(t, ".", dir)

	// The short name is the same interface.
	short, err := hostmodule.Define(source(map[string]string{"shapes.wit": everyType}), ".", "all")
	require.NoError(t, err)
	require.Equal(t, d.Name(), short.Name())
}

func TestTauOwnInterfacesAreReadFromTheWITGuestsAreBuiltAgainst(t *testing.T) {
	d, err := hostmodule.Define(witfs.FS, "deps/taubyte-secret", "taubyte:secret/vault@0.1.0")
	require.NoError(t, err)
	require.Equal(t, []string{"get"}, d.Funcs())
}

func TestWhatDefineRefuses(t *testing.T) {
	type testCase struct {
		name, wit, iface, says string
	}
	for _, tc := range []testCase{
		{"no such interface", everyType, "acme:shapes/none@1.0.0", "declares no interface"},
		{"not WIT", "package acme:x@1.0.0;\ninterface i { f: func( }", "i", "hostmodule:"},
		{"a resource", "package acme:x@1.0.0;\ninterface i { resource r { constructor(); } }", "i", "declares a resource"},
		{"a map", "package acme:x@1.0.0;\ninterface i { f: func(m: map<string, u32>); }", "i", "map"},
		{"an async func", "package acme:x@1.0.0;\ninterface i { f: async func(); }", "i", "async"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := hostmodule.Define(source(map[string]string{"x.wit": tc.wit}), ".", tc.iface)
			require.ErrorContains(t, err, tc.says)
		})
	}

	require.Panics(t, func() { hostmodule.MustDefine(source(map[string]string{"x.wit": everyType}), ".", "none") })
}

func TestAModuleImplementsExactlyItsInterface(t *testing.T) {
	d := hostmodule.MustDefine(source(map[string]string{"shapes.wit": everyType}), ".", "all")
	noop := func(context.Context, []hostmodule.Value) (hostmodule.Value, error) { return nil, nil }

	_, err := d.Implement(map[string]hostmodule.Func{"echo": noop})
	require.ErrorIs(t, err, hostmodule.ErrIncomplete)
	require.ErrorContains(t, err, "nothing is not implemented")

	_, err = d.Implement(map[string]hostmodule.Func{"echo": noop, "nothing": noop, "extra": noop})
	require.ErrorIs(t, err, hostmodule.ErrIncomplete)
	require.ErrorContains(t, err, "declares no extra")

	m, err := d.Implement(map[string]hostmodule.Func{"echo": noop, "nothing": noop})
	require.NoError(t, err)
	require.Same(t, d, m.Definition)
}

func TestResultsAreBuiltByArm(t *testing.T) {
	require.Equal(t, hostmodule.Result{Payload: "v"}, hostmodule.Ok("v"))
	require.Equal(t, hostmodule.Result{Err: true, Payload: "e"}, hostmodule.Fail("e"))
}
