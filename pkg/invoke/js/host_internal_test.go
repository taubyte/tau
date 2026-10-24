package js

import (
	"errors"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestDecodeUTF8IsTheWHATWGDecoder(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"ascii", []byte("abc"), "abc"},
		{"every width", []byte("é€😀"), "é€😀"},
		{"a stray continuation", []byte{0x80}, "�"},
		{"an invalid lead", []byte{0xc0, 0x41}, "�A"},
		{"a maximal subpart", []byte{0xe2, 0x82, 0x41}, "�A"},
		{"an overlong three-byte form", []byte{0xe0, 0x80, 0x80}, "���"},
		{"a surrogate", []byte{0xed, 0xa0, 0x80}, "���"},
		{"past U+10FFFF", []byte{0xf4, 0x90, 0x80, 0x80}, "����"},
		{"an overlong four-byte form", []byte{0xf0, 0x80, 0x80, 0x80}, "����"},
		{"the largest code point", []byte{0xf4, 0x8f, 0xbf, 0xbf}, "\U0010FFFF"},
		{"truncated at the end", []byte{0x41, 0xf0, 0x9f, 0x98}, "A�"},
		{"empty", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, rest, ok := decodeUTF8(c.in, false, false)
			require.True(t, ok)
			require.Zero(t, rest)
			require.Equal(t, c.want, got)
			require.True(t, utf8.ValidString(got))

			_, _, ok = decodeUTF8(c.in, true, false)
			require.Equal(t, utf8.Valid(c.in), ok, "fatal refuses exactly what is not UTF-8")
		})
	}
}

func TestDecodeUTF8HoldsBackAnIncompleteTail(t *testing.T) {
	got, rest, ok := decodeUTF8([]byte{0x41, 0xf0, 0x9f, 0x98}, true, true)
	require.True(t, ok)
	require.Equal(t, "A", got)
	require.Equal(t, 3, rest)

	got, rest, ok = decodeUTF8([]byte{0xf0, 0x9f, 0x98, 0x80}, true, true)
	require.True(t, ok)
	require.Equal(t, "😀", got)
	require.Zero(t, rest)

	_, _, ok = decodeUTF8([]byte{0xff, 0xf0}, true, true)
	require.False(t, ok, "an invalid byte before the tail is still refused")
}

func TestWellFormedReplacesOnlyLoneSurrogates(t *testing.T) {
	require.Equal(t, []byte("plain é"), wellFormed("plain é"))
	require.Equal(t, []byte("퟿"), wellFormed("퟿"), "U+D7FF starts with 0xed and is no surrogate")
	require.Equal(t, []byte("a�b"), wellFormed("a\xed\xa0\x80b"))
	require.Equal(t, []byte("�"), wellFormed("\xed\xbf\xbf"))
	require.Equal(t, []byte("\xed\xa0"), wellFormed("\xed\xa0"), "a cut-off sequence is left as it is")
}

func TestFacadeTurnsAPanicBackIntoAnError(t *testing.T) {
	boom := errors.New("the arena is full")
	require.ErrorIs(t, facade(func() error { panic(boom) }), boom)
	require.ErrorContains(t, facade(func() error { panic("engine: missing helper") }), "the engine panicked: engine: missing helper")
	require.NoError(t, facade(func() error { return nil }))
}

func TestTheStackLeavesTheHeapRoom(t *testing.T) {
	require.Equal(t, maxStack, stackFor(256<<20))
	require.Equal(t, 64<<10, stackFor(512<<10))
}
