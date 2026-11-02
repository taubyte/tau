package wasm

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/samyfodil/wazy/component"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke/hostmodule"
)

const shapes = `package acme:shapes@1.0.0;

interface all {
    record point { x: s32, y: s32 }
    variant shape { dot, circle(u32), named(string) }
    enum colour { red, green, blue }
    flags access { read, write, exec }

    echo: func(p: point, s: shape, c: colour, a: access, o: option<string>, t: tuple<u8, string>, b: list<u8>, n: list<string>, u: list<u32>) -> result<list<point>, shape>;
    nothing: func();
}
`

func shapesDefinition(t *testing.T) *hostmodule.Definition {
	t.Helper()
	d, err := hostmodule.Define(fstest.MapFS{"shapes.wit": &fstest.MapFile{Data: []byte(shapes)}}, ".", "all")
	require.NoError(t, err)
	return d
}

func TestEveryTypeRoundTripsThroughWazysShape(t *testing.T) {
	sig := shapesDefinition(t).Signature("echo")
	resolve := sig.Types.Resolver()

	args := []hostmodule.Value{
		hostmodule.Record{"x": int32(-1), "y": int32(2)},
		hostmodule.Variant{Case: "circle", Payload: uint32(7)},
		"green",
		[]string{"read", "exec"},
		"some",
		[]hostmodule.Value{uint32(1), "one"},
		[]byte("raw"),
		[]hostmodule.Value{"a", "b"},
		[]hostmodule.Value{uint32(3), uint32(4)},
	}
	wazy := []component.Value{
		[]component.Value{int32(-1), int32(2)},
		component.VariantValue{Disc: 1, Payload: uint32(7)},
		uint32(1),
		uint32(0b101),
		"some",
		[]component.Value{uint32(1), "one"},
		[]byte("raw"),
		[]component.Value{"a", "b"},
		[]component.Value{uint32(3), uint32(4)},
	}
	for i, p := range sig.Desc.Params {
		lowered, err := lower(p.Type, resolve, args[i])
		require.NoError(t, err, p.Name)
		require.Equal(t, wazy[i], lowered, p.Name)

		lifted, err := lift(p.Type, resolve, wazy[i])
		require.NoError(t, err, p.Name)
		require.Equal(t, args[i], lifted, p.Name)
	}

	// wazy lifts a list of a fixed-width primitive to its typed slice.
	lifted, err := lift(sig.Desc.Params[8].Type, resolve, []uint32{3, 4})
	require.NoError(t, err)
	require.Equal(t, []hostmodule.Value{uint32(3), uint32(4)}, lifted)

	// None is nil both ways.
	none, err := lower(sig.Desc.Params[4].Type, resolve, nil)
	require.NoError(t, err)
	require.Nil(t, none)
	none, err = lift(sig.Desc.Params[4].Type, resolve, nil)
	require.NoError(t, err)
	require.Nil(t, none)

	result := *sig.Desc.Results.Unnamed
	for _, r := range []hostmodule.Result{
		hostmodule.Ok([]hostmodule.Value{hostmodule.Record{"x": int32(0), "y": int32(0)}}),
		hostmodule.Fail(hostmodule.Variant{Case: "dot"}),
		hostmodule.Fail(hostmodule.Variant{Case: "named", Payload: "n"}),
	} {
		lowered, err := lower(result, resolve, r)
		require.NoError(t, err)
		back, err := lift(result, resolve, lowered)
		require.NoError(t, err)
		require.Equal(t, r, back)
	}
}

func TestAValueOfTheWrongShapeIsRefusedNamingIt(t *testing.T) {
	sig := shapesDefinition(t).Signature("echo")
	resolve := sig.Types.Resolver()
	param := func(i int) component.TypeRef { return sig.Desc.Params[i].Type }

	type testCase struct {
		name string
		at   component.TypeRef
		v    hostmodule.Value
		says string
	}
	for _, tc := range []testCase{
		{"a record that is not one", param(0), "x", "a record is a hostmodule.Record"},
		{"a record missing a field", param(0), hostmodule.Record{"x": int32(1)}, "the record has no y"},
		{"a field of the wrong type", param(0), hostmodule.Record{"x": 1, "y": int32(1)}, "x: a s32 is not a int"},
		{"a variant that is not one", param(1), "dot", "a variant is a hostmodule.Variant"},
		{"a case it does not have", param(1), hostmodule.Variant{Case: "square"}, `no case "square"`},
		{"a payload of the wrong type", param(1), hostmodule.Variant{Case: "named", Payload: 3}, "named: a string is not a int"},
		{"an enum case it does not have", param(2), "purple", "the enum has no case purple"},
		{"flags that are not names", param(3), uint32(1), "flags are a []string"},
		{"a flag it does not have", param(3), []string{"admin"}, `no flag "admin"`},
		{"a tuple of the wrong length", param(5), []hostmodule.Value{uint32(1)}, "a tuple of 1, want 2"},
		{"a list that is not one", param(7), "a", "a list or tuple is a []hostmodule.Value"},
		{"an element of the wrong type", param(7), []hostmodule.Value{1}, "[0]: a string is not a int"},
		{"a result that is not one", *sig.Desc.Results.Unnamed, "ok", "a result is a hostmodule.Result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := lower(tc.at, resolve, tc.v)
			require.ErrorContains(t, err, tc.says)
		})
	}

	// And what wazy hands over that does not fit the WIT.
	for _, tc := range []testCase{
		{"a record", param(0), "x", "a record is string"},
		{"a variant past its cases", param(1), component.VariantValue{Disc: 9}, "a variant is"},
		{"an enum past its cases", param(2), uint32(9), "an enum is 9"},
		{"flags past their names", param(3), uint32(1 << 5), "flag 5 of 3"},
		{"flags of the wrong type", param(3), "read", "flags are string"},
		{"a list", param(7), "a", "a list is string"},
		{"a result", *sig.Desc.Results.Unnamed, "ok", "a result is string"},
	} {
		t.Run("lifting "+tc.name, func(t *testing.T) {
			_, err := lift(tc.at, resolve, tc.v)
			require.ErrorContains(t, err, tc.says)
		})
	}
}

func TestEveryPrimitiveIsItsOwnShape(t *testing.T) {
	for prim, v := range map[string]any{
		"bool": true, "s8": int32(1), "u16": uint32(1), "s64": int64(1), "u64": uint64(1),
		"f32": float32(1), "f64": float64(1), "char": 'x', "string": "s",
	} {
		got, err := primitive(prim, v)
		require.NoError(t, err, prim)
		require.Equal(t, v, got)
	}
	_, err := primitive("u64", 1)
	require.ErrorContains(t, err, "a u64 is not a int")
}

func TestAttachRegistersEveryFunction(t *testing.T) {
	d := shapesDefinition(t)
	noop := func(context.Context, []hostmodule.Value) (hostmodule.Value, error) { return nil, nil }
	m, err := d.Implement(map[string]hostmodule.Func{"echo": noop, "nothing": noop})
	require.NoError(t, err)
	require.Len(t, attach(m), 2)
}
