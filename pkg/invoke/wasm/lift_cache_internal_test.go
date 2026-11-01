package wasm

import (
	"testing"

	"github.com/samyfodil/wazy/component"
	"github.com/stretchr/testify/require"
)

// The guest-to-host boundary of the cache world: a guest is untrusted, so every
// lift refuses a shape it does not understand. Widened scalars are legal per
// wazy's ListDesc.

func TestTwoStringsRefusesAnythingElse(t *testing.T) {
	first, second, err := twoStrings([]component.Value{"sessions", "abc"})
	require.NoError(t, err)
	require.Equal(t, "sessions", first)
	require.Equal(t, "abc", second)

	for _, one := range []struct {
		name string
		args []component.Value
		says string
	}{
		{"no arguments", nil, "got 0"},
		{"one argument", []component.Value{"sessions"}, "got 1"},
		{"first is not a string", []component.Value{uint32(1), "abc"}, "first argument is uint32"},
		{"second is not a string", []component.Value{"sessions", uint32(1)}, "second argument is uint32"},
	} {
		t.Run(one.name, func(t *testing.T) {
			_, _, err := twoStrings(one.args)
			require.ErrorContains(t, err, one.says)
		})
	}
}

func TestLiftU32TakesEveryShapeThatCrossesTheABI(t *testing.T) {
	for _, one := range []struct {
		name  string
		value component.Value
		want  uint32
	}{
		{"u32", uint32(7), 7},
		{"widened to u64", uint64(7), 7},
		{"signed 32", int32(7), 7},
		{"signed 64", int64(7), 7},
		{"absent is zero", nil, 0},
	} {
		t.Run(one.name, func(t *testing.T) {
			got, err := liftU32(one.value)
			require.NoError(t, err)
			require.Equal(t, one.want, got)
		})
	}

	_, err := liftU32("7")
	require.ErrorContains(t, err, "expected a u32, got string",
		"a string that happens to look like a number is not one")
}

func TestByteListNeverLowersNothing(t *testing.T) {
	require.Equal(t, []byte{}, byteList(nil))
	require.NotNil(t, byteList(nil), "an empty list, not a nil value")
	require.Equal(t, []byte("abc"), byteList([]byte("abc")))
	require.Equal(t, []byte{}, byteList([]byte{}))
}

func TestReadBytesTakesBothListShapes(t *testing.T) {
	// The specialised shape, which is what a guest's own vector produces.
	got, err := readBytes([]byte("abc"))
	require.NoError(t, err)
	require.Equal(t, []byte("abc"), got)

	// The general one: a list that came from somewhere other than a guest's
	// vector is still a list.
	got, err = readBytes([]component.Value{uint8('a'), uint8('b')})
	require.NoError(t, err)
	require.Equal(t, []byte("ab"), got)

	got, err = readBytes([]component.Value{})
	require.NoError(t, err)
	require.Empty(t, got)

	// AND THE ELEMENT THAT IS NOT A BYTE IS NAMED BY POSITION, because a guest
	// author reading "element 1" can find it and "expected a list" cannot.
	_, err = readBytes([]component.Value{uint8('a'), "b"})
	require.ErrorContains(t, err, "element 1 is string, want u8")

	_, err = readBytes(uint32(5))
	require.ErrorContains(t, err, "expected a list, got uint32")
}
